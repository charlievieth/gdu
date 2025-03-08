package analyze

import (
	gofs "io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/charlievieth/fastwalk"
	"github.com/dundee/gdu/v5/internal/common"
	"github.com/dundee/gdu/v5/pkg/fs"
	log "github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"
)

// ParallelAnalyzer implements Analyzer
type ParallelAnalyzer struct {
	dirs      sync.Map
	conf      *fastwalk.Config
	progress  *common.AtomicProgress
	doneChan  common.SignalGroup
	ignoreDir common.ShouldDirBeIgnored
}

// CreateAnalyzer returns Analyzer
func CreateAnalyzer() *ParallelAnalyzer {
	conf := fastwalk.DefaultConfig.Copy()
	// WARN: this needs to be tuned !!!
	conf.Sort = fastwalk.SortFilesFirst
	conf.ToSlash = true
	conf.NumWorkers = 10 // TODO: make this configurable
	conf.Follow = false
	return &ParallelAnalyzer{
		conf: conf,
		// conf: fastwalk.DefaultConfig.Copy(),
		progress: &common.AtomicProgress{},
		// TODO: might want to make these bigger or drop
		// any queued progress. Making this an atomic
		// pointer that we occaisonally check would also
		// work.
		doneChan: make(common.SignalGroup),
	}
}

// SetFollowSymlinks sets whether symlink to files should be followed
func (a *ParallelAnalyzer) SetFollowSymlinks(v bool) {
	a.conf.Follow = v
}

// GetCurrentProgress returns the current scan progress and is safe
// to call concurrently.
func (a *ParallelAnalyzer) GetCurrentProgress() common.CurrentProgress {
	return a.progress.CurrentProgress()
}

// GetDone returns channel for checking when analysis is done
func (a *ParallelAnalyzer) GetDone() common.SignalGroup {
	return a.doneChan
}

// ResetProgress returns progress
func (a *ParallelAnalyzer) ResetProgress() {
	a.progress = a.progress.Reset()
	a.doneChan = make(common.SignalGroup)
}

// AnalyzeDir analyzes given path
func (a *ParallelAnalyzer) AnalyzeDir(
	path string, ignore common.ShouldDirBeIgnored, constGC bool,
) fs.Item {
	if !constGC {
		defer debug.SetGCPercent(debug.SetGCPercent(-1))
		go manageMemoryUsage(a.doneChan)
	}

	a.ignoreDir = ignore

	clean := filepath.ToSlash(filepath.Clean(path))
	if err := fastwalk.Walk(a.conf, clean, a.walk); err != nil {
		log.Println(err)
	}

	// dir, ok := a.loadDir(filepath.Clean(path))
	dir, ok := a.loadDir(clean)
	if !ok {
		log.Errorf("missing root directory: %s\n", clean)
		return nil
	}

	// TODO: could we recursively do this using the root dir?
	// start = time.Now()
	a.dirs.Range(func(_, v any) bool {
		dd := v.(*Dir)
		// TODO: check for loops with: `dd.Parent == dd` ???
		files := dd.GetFiles()
		dd.Flag = getDirFlag(nil, len(files))

		// TODO: make sure that we don't need this only for tests !!!
		//
		if len(files) > 1 {
			slices.SortFunc(files, func(f1, f2 fs.Item) int {
				return strings.Compare(f1.GetName(), f2.GetName())
			})
			dd.SetFiles(files)
		}
		return true
	})
	dir.BasePath = filepath.Dir(clean)

	a.doneChan.Broadcast()

	// WARN: we should probably clear the sync map
	a.dirs.Clear()
	a.dirs = sync.Map{}

	return dir
}

func getDirFlag(err error, items int) rune {
	switch {
	case err != nil:
		return '!'
	case items == 0:
		return 'e'
	default:
		return ' '
	}
}

func (a *ParallelAnalyzer) storeDir(path string, dir *Dir) {
	a.dirs.Store(path, dir)
}

func (a *ParallelAnalyzer) loadDir(path string) (*Dir, bool) {
	if v, _ := a.dirs.Load(path); v != nil {
		return v.(*Dir), true
	}
	return nil, false
}

func (a *ParallelAnalyzer) walk(path string, de gofs.DirEntry, err error) error {
	if err != nil {
		if os.IsPermission(err) {
			return nil
		}
		return err
	}

	// path = filepath.Clean(path)
	dirname, basename := filepath.Split(path)
	if n := len(dirname); n > 1 && dirname[n-1] == '/' {
		dirname = dirname[:n-1] // Trim trailing slash
	}

	// WARN: this cuts down one memory use at the cost of time
	// basename = strings.Clone(basename)

	// TODO: maybe check for symlink here instead of below
	if de.IsDir() {
		if a.ignoreDir != nil && a.ignoreDir(basename, path) {
			return fastwalk.SkipDir
		}
		dir := &Dir{
			File: &File{
				Name: basename,
			},
			ItemCount: 1,
		}
		setDirPlatformSpecificAttrs(dir, path)
		// a.storeDir(filepath.Clean(path), dir)
		a.storeDir(path, dir)
		// if parent, ok := a.loadDir(filepath.Dir(path)); ok {
		if len(dirname) < len(path) {
			if parent, ok := a.loadDir(dirname); ok {
				dir.Parent = parent
				parent.AddFileLocked(dir)
			}
		}

		// Only update name for directories to prevent churn
		a.progress.CurrentItemName.Store(&path)
	} else {
		dir, ok := a.loadDir(dirname)
		if !ok {
			log.Errorf("missing directory: %s\n", path)
			return nil
		}
		// TOOD: Explain why we don't use de.Info() here
		info, err := os.Lstat(path)
		if err != nil {
			log.Error(err)
			dir.SetFlag('!')
			return nil
		}
		// WARN: I really think we need to check for symlink at the top
		// otherwise we may lookup the wrong thing
		if a.conf.Follow && info.Mode()&os.ModeSymlink != 0 {
			// TODO: the other code uses followSymlink() which seems excessive
			fi, err := fastwalk.StatDirEntry(path, de)
			if err != nil {
				log.Error(err)
				dir.SetFlag('!')
				return nil
			}
			// Ignore symlinks since they will be visited again (WARN: is that correct?)
			if fi.IsDir() {
				return nil
			}
			info = fi
		}

		file := &File{
			Name:   basename,
			Flag:   getFlag(info),
			Size:   info.Size(),
			Parent: dir,
		}
		setPlatformSpecificAttrs(file, info)
		dir.AddFileLocked(file)

		a.progress.TotalSize.Add(info.Size())
	}

	a.progress.ItemCount.Add(1)
	return nil
}

func getFlag(f os.FileInfo) rune {
	// TODO: Add a flag for broken symlinks
	if f.Mode()&os.ModeSymlink != 0 || f.Mode()&os.ModeSocket != 0 {
		return '@'
	}
	return ' '
}

func followSymlink(path string) (os.FileInfo, error) {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	tInfo, err := os.Lstat(target)
	if err != nil {
		return nil, err
	}
	if !tInfo.IsDir() {
		return tInfo, nil
	}
	return nil, nil
}

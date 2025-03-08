package common

import (
	"sync/atomic"

	"github.com/dundee/gdu/v5/pkg/fs"
)

// CurrentProgress struct
type CurrentProgress struct {
	CurrentItemName string
	ItemCount       int
	TotalSize       int64
}

type AtomicProgress struct {
	CurrentItemName atomic.Pointer[string]
	ItemCount       atomic.Int64
	TotalSize       atomic.Int64
}

func (p *AtomicProgress) Reset() *AtomicProgress {
	if p == nil {
		return new(AtomicProgress)
	}
	*p = AtomicProgress{}
	return p
}

func (p *AtomicProgress) CurrentProgress() CurrentProgress {
	var name string
	if s := p.CurrentItemName.Load(); s != nil {
		name = *s
	}
	return CurrentProgress{
		CurrentItemName: name,
		ItemCount:       int(p.ItemCount.Load()),
		TotalSize:       p.TotalSize.Load(),
	}
}

// ShouldDirBeIgnored whether path should be ignored
type ShouldDirBeIgnored func(name, path string) bool

// Analyzer is type for dir analyzing function
type Analyzer interface {
	AnalyzeDir(path string, ignore ShouldDirBeIgnored, constGC bool) fs.Item
	SetFollowSymlinks(bool)
	GetCurrentProgress() CurrentProgress
	GetDone() SignalGroup
	ResetProgress()
}

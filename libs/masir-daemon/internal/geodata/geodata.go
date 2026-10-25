// Package geodata lists the categories in geosite.dat and geoip.dat, so a UI can offer
// every one of them, not just the few it knows by name.
package geodata

import (
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
)

// Category is one list in a geo file: a geosite: or geoip: code, and how many domains
// or address ranges it holds.
type Category struct {
	Code    string
	Entries int
}

var cache struct {
	sync.Mutex
	entries map[string]cached
}

type cached struct {
	mod  time.Time
	size int64
	cats []Category
}

// Categories lists the categories in the geo file at path, in file order. Both files
// share a layout: a list of entries, each a code (field 1) and its items (field 2).
// Only codes are decoded and items counted, so a large file scans quickly; results are
// cached until the file changes.
func Categories(path string) ([]Category, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	cache.Lock()
	defer cache.Unlock()
	if c, ok := cache.entries[path]; ok && c.mod.Equal(fi.ModTime()) && c.size == fi.Size() {
		return c.cats, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cats, err := scan(b)
	if err != nil {
		return nil, err
	}
	if cache.entries == nil {
		cache.entries = map[string]cached{}
	}
	cache.entries[path] = cached{mod: fi.ModTime(), size: fi.Size(), cats: cats}
	return cats, nil
}

var errMalformed = errors.New("not a valid geo data file")

func scan(b []byte) ([]Category, error) {
	var cats []Category
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return nil, errMalformed
		}
		b = b[n:]
		if num != 1 || typ != protowire.BytesType { // not an entry: skip it
			n = protowire.ConsumeFieldValue(num, typ, b)
			if n < 0 {
				return nil, errMalformed
			}
			b = b[n:]
			continue
		}
		entry, n := protowire.ConsumeBytes(b)
		if n < 0 {
			return nil, errMalformed
		}
		b = b[n:]
		c, err := scanEntry(entry)
		if err != nil {
			return nil, err
		}
		cats = append(cats, c)
	}
	return cats, nil
}

func scanEntry(b []byte) (Category, error) {
	var c Category
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return c, errMalformed
		}
		b = b[n:]
		if num == 1 && typ == protowire.BytesType {
			code, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return c, errMalformed
			}
			c.Code = strings.ToLower(string(code))
			b = b[n:]
			continue
		}
		if num == 2 {
			c.Entries++
		}
		n = protowire.ConsumeFieldValue(num, typ, b)
		if n < 0 {
			return c, errMalformed
		}
		b = b[n:]
	}
	return c, nil
}

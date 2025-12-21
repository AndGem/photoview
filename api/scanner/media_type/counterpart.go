package media_type

import (
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// FindWebCounterpart returns the filename if the file `imagePath` has a counterpart file competible with the browser.
func FindWebCounterpart(imagePath string) (string, bool) {
	return findCounterpart(imagePath, func(filename string) bool {
		mt := GetMediaType(filename)
		return mt.IsImage() && mt.IsWebCompatible()
	})
}

// FindRawCounterpart returns the filename if the file `imagePath` has a counterpart file which needs to be processed before showing in the browser.
func FindRawCounterpart(imagePath string) (string, bool) {
	return findCounterpart(imagePath, func(filename string) bool {
		mt := GetMediaType(filename)
		return mt.IsImage() && !mt.IsWebCompatible()
	})
}

var globCache sync.Map // map[string][]string

func findCounterpart(filename string, acceptFn func(filepath string) bool) (string, bool) {
	ext := path.Ext(filename)
	pattern := strings.TrimSuffix(filename, ext) + ".*"

	var filenames []string
	if v, ok := globCache.Load(pattern); ok {
		filenames = v.([]string)
	} else {
		var err error
		filenames, err = filepath.Glob(pattern)
		if err != nil {
			return "", false
		}
		globCache.Store(pattern, filenames)
	}

	for _, f := range filenames {
		if f != filename && acceptFn(f) {
			return f, true
		}
	}

	return "", false
}

package library

import (
	"testing"

	"github.com/fhs/gompd/v2/mpd"
)

func TestEntryFromHidesTempDir(t *testing.T) {
	if _, ok := entryFrom(mpd.Attrs{"directory": TempDir}); ok {
		t.Fatal("the upload folder must not show in the Library")
	}
	if e, ok := entryFrom(mpd.Attrs{"directory": "Jazz/" + TempDir}); !ok || e.Name != TempDir {
		t.Fatal("only the folder in the music root is hidden")
	}
	if e, ok := entryFrom(mpd.Attrs{"directory": "Rock"}); !ok || e.Type != "directory" {
		t.Fatalf("got %+v", e)
	}
}

package query

import (
	"reflect"
	"testing"

	"github.com/muralx/codedoc/internal/output"
)

func TestUnseenDropsTheLinesAnEarlierHitPrinted(t *testing.T) {
	shown := shownLines{}
	f := fileRef{1, 7}
	first := []output.Fragment{{Start: 3, End: 6, Text: "c\nd\ne\nf\n"}}
	if got := shown.unseen(f, first); !reflect.DeepEqual(got, first) {
		t.Fatalf("nothing was shown yet: %+v", got)
	}
	tests := []struct {
		name string
		file fileRef
		in   output.Fragment
		want []output.Fragment
	}{
		{"inside what was shown", f, output.Fragment{Start: 4, End: 5, Text: "d\ne\n"}, nil},
		{"mostly shown", f, output.Fragment{Start: 4, End: 7, Text: "d\ne\nf\ng\n"}, nil},
		{"shown at its start", f, output.Fragment{Start: 6, End: 9, Text: "f\ng\nh\ni\n"},
			[]output.Fragment{{Start: 7, End: 9, Text: "g\nh\ni\n"}}},
		{"shown at its end", f, output.Fragment{Start: 1, End: 3, Text: "a\nb\nc\n"},
			[]output.Fragment{{Start: 1, End: 2, Text: "a\nb\n"}}},
		{"another file", fileRef{1, 8}, output.Fragment{Start: 4, End: 5, Text: "d\ne\n"},
			[]output.Fragment{{Start: 4, End: 5, Text: "d\ne\n"}}},
	}
	for _, tt := range tests {
		if got := shown.unseen(tt.file, []output.Fragment{tt.in}); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: got %+v, want %+v", tt.name, got, tt.want)
		}
	}
	// What a later hit kept counts as shown too.
	if got := shown.unseen(f, []output.Fragment{{Start: 8, End: 9, Text: "h\ni\n"}}); got != nil {
		t.Errorf("lines 8-9 were printed by an earlier hit: %+v", got)
	}
}

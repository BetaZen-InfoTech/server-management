package handlers

import (
	"reflect"
	"testing"
)

// TestSplitParagraphs covers the body-text → paragraph-list split that
// the home-page template walks. Important behaviours:
//   - blank-line-separated blocks become separate <p>s
//   - trailing/leading whitespace is trimmed per block
//   - empty input yields a nil slice so the template can skip the
//     whole .body div
//   - CRLF line endings (operator pasting from Word / a Windows editor)
//     normalise the same as Unix LF
func TestSplitParagraphs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", []string{}},
		{"whitespace only", "   \n\n  \t\n", []string{}},
		{"single line", "Hello world.", []string{"Hello world."}},
		{"two paragraphs", "First.\n\nSecond.", []string{"First.", "Second."}},
		{"trims surrounding whitespace", "  First.  \n\n  Second.  ", []string{"First.", "Second."}},
		{"crlf normalises", "First.\r\n\r\nSecond.", []string{"First.", "Second."}},
		{"multiple blanks collapse", "First.\n\n\n\nSecond.", []string{"First.", "Second."}},
		{"single newline stays in paragraph", "Line one\nLine two.", []string{"Line one\nLine two."}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitParagraphs(tc.in)
			// Normalise nil vs empty slice for the test — both render
			// the same in the template (the `if .BodyParagraphs` check
			// treats len==0 as falsy regardless of nil-ness).
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("splitParagraphs(%q) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

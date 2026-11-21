package protect

import (
	"fmt"
	"testing"
)

func TestHostRunesRefuseNoncharacters(t *testing.T) {
	blocks := [][2]rune{{0xFDD0, 0xFDEF}}
	for plane := range rune(17) {
		blocks = append(blocks, [2]rune{plane<<16 | 0xFFFE, plane<<16 | 0xFFFF})
	}
	count := 0
	for _, b := range blocks {
		name := fmt.Sprintf("U+%04X..U+%04X", b[0], b[1])
		for r := b[0]; r <= b[1]; r++ {
			count++
			if hostRunes(string(r)) || hostRunes("a"+string(r)) || hostRunes(string(r)+"\u00e9") {
				t.Errorf("noncharacter block %s accepted: U+%04X", name, r)
			}
		}
	}
	if count != 66 {
		t.Fatalf("%d noncharacters, want 66", count)
	}
	for _, s := range []string{"a", "\u00e9", "\u00df", "\u4e2d", "\U0001f600", "\uFDCF", "\uFDF0", "\uFFFD"} {
		if !hostRunes(s) {
			t.Errorf("hostRunes(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"\U0001E5D0", "\U0001E6C0", "\u0378"} {
		if hostRunes(s) {
			t.Errorf("hostRunes(%q) = true for a code point not assigned in Unicode 15.0", s)
		}
	}
}

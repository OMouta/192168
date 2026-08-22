package invite

import "testing"

func TestParseTakesACodeOutOfWhateverWasPasted(t *testing.T) {
	const want = "k7m2n9xq"

	for _, input := range []string{
		"k7m2n9xq",
		"K7M2N9XQ",
		"  k7m2-n9xq  ",
		"https://192168.lol/join/k7m2n9xq",
		"https://192168.lol/join/k7m2n9xq/",
		"https://api.example.com/join/K7M2N9XQ?from=discord",
		"192168://join/k7m2n9xq",
	} {
		if got := Parse(input); got != want {
			t.Errorf("Parse(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeKeepsOnlyWhatACodeIsMadeOf(t *testing.T) {
	if got := Normalize("  A-b_c 1! "); got != "abc1" {
		t.Errorf("Normalize = %q, want abc1", got)
	}
	if got := Normalize("----"); got != "" {
		t.Errorf("Normalize of punctuation alone = %q, want empty", got)
	}
}

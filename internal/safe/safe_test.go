package safe

import "testing"

func TestText(t *testing.T) {
	cases := map[string]string{
		"done \x1b]2;PWNED\x07 ok":       "done ]2;PWNED ok",
		"\x1b]52;c;ZXZpbA==\x07copied":   "]52;c;ZXZpbA==copied",
		"red \x1b[41mRED\x1b[0m":         "red [41mRED[0m",
		"c1 \u009b31m csi":               "c1 31m csi",
		"invoice‮txt.exe":                "invoicetxt.exe",
		"tab\tand\nnewline":              "tab and newline",
		"zażółć gęślą jaźń · ✨ YUM-4331": "zażółć gęślą jaźń · ✨ YUM-4331",
	}
	for in, want := range cases {
		if got := Text(in); got != want {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
	}
}

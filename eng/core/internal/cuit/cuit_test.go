package cuit

import "testing"

// Synthetic CUITs only (all zeros pattern), never real ones.
func TestParse(t *testing.T) {
	for s, ok := range map[string]bool{
		"30000000015": true,  // weighted sum 17, 17 mod 11 = 6, 11-6 = 5
		"30000000023": true,  // weighted sum 19, mod 11 = 8, 11-8 = 3
		"20000000028": true,  // weighted sum 14, mod 11 = 3, 11-3 = 8
		"27000000006": true,  // weighted sum 38, mod 11 = 5, 11-5 = 6
		"20000000060": true,  // weighted sum 22, mod 11 = 0: 11 maps to 0
		"20000000010": false, // weighted sum 12, mod 11 = 1: 10 is never issued
		"20000000061": false, // wrong check digit
		"20000000029": false, // wrong check digit
		"2000000002":  false, // 10 digits
		"2000000002a": false,
		"":            false,
	} {
		_, err := Parse(s)
		if (err == nil) != ok {
			t.Errorf("Parse(%q): err=%v, want ok=%v", s, err, ok)
		}
	}
}

func TestValid(t *testing.T) {
	if !Valid(30000000015) || Valid(30000000016) || Valid(123) || Valid(-30000000015) {
		t.Fatal("Valid disagrees with Parse")
	}
}

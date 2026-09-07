package user

import "testing"

func TestIsCommonWeakPassword(t *testing.T) {
	cases := []struct {
		name     string
		password string
		want     bool
	}{
		{"exact match", "welcome1", true},
		{"case-insensitive match", "WELCOME1", true},
		{"mixed-case match", "Password1!", true},
		{"not on the list", "correct horse battery staple", false},
		{"password123 is deliberately not on the list", "password123", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCommonWeakPassword(tc.password); got != tc.want {
				t.Errorf("isCommonWeakPassword(%q) = %v, want %v", tc.password, got, tc.want)
			}
		})
	}
}

func TestIsSingleRepeatedRune(t *testing.T) {
	cases := []struct {
		name     string
		password string
		want     bool
	}{
		{"all same letter", "aaaaaaaa", true},
		{"all same digit", "11111111", true},
		{"all spaces", "        ", true},
		{"single character", "a", true},
		{"empty string", "", true},
		{"mixed characters", "aaaaaaab", false},
		{"ordinary password", "correct horse battery staple", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSingleRepeatedRune(tc.password); got != tc.want {
				t.Errorf("isSingleRepeatedRune(%q) = %v, want %v", tc.password, got, tc.want)
			}
		})
	}
}

func TestIsSequentialRun(t *testing.T) {
	cases := []struct {
		name     string
		password string
		want     bool
	}{
		{"ascending digits", "12345678", true},
		{"descending digits", "87654321", true},
		{"ascending letters", "abcdefgh", true},
		{"descending letters", "hgfedcba", true},
		{"single character", "a", false},
		{"empty string", "", false},
		{"broken run", "12345679", false},
		{"repeated, not sequential", "aaaaaaaa", false},
		{"ordinary password", "correct horse battery staple", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSequentialRun(tc.password); got != tc.want {
				t.Errorf("isSequentialRun(%q) = %v, want %v", tc.password, got, tc.want)
			}
		})
	}
}

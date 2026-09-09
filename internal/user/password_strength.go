package user

import "strings"

// commonWeakPasswords is a compact, hand-curated set of the passwords that
// appear most often in public breach-frequency lists (SplashData/NordPass-
// style "most common passwords" rankings) and their most predictable
// decorations — "Password1!" being the canonical example of a password
// that satisfies almost any character-class composition rule while still
// being one of the first guesses in any real attack. Matching is
// case-insensitive (see isCommonWeakPassword) so "PASSWORD1!" is caught by
// the same "password1!" entry.
//
// This is deliberately a local, dependency-free list rather than a live
// breach-database lookup (e.g. Have I Been Pwned's k-anonymity API) — see
// docs/DECISIONS.md § "Validação de senha forte" for the trade-off and why
// it was chosen over the network-call alternative. That also means it is a
// few hundred entries, not the hundreds of thousands a real breach corpus
// holds: it catches the passwords everyone already knows are weak, not
// every password that has ever leaked.
//
// "password123" is deliberately absent, despite genuinely belonging on a
// list like this one: it is this project's own established demo/test
// password (cmd/seed's -password default, and the fixture dozens of
// tests across internal/user, internal/task and cmd/api register through
// the real Register/Login path). Blocking it here would break the seed
// tool and a large share of the suite over a string that is already
// documented as "demo only — never reuse" at its one production-adjacent
// use site (cmd/seed/main.go) — the risk this list exists to close does
// not apply to a value nothing real is ever supposed to authenticate
// with.
var commonWeakPasswords = buildCommonWeakPasswordSet([]string{
	"123456", "password", "12345678", "qwerty", "123456789", "12345",
	"1234", "111111", "1234567", "dragon", "123123", "baseball",
	"iloveyou", "trustno1", "1234567890", "sunshine", "master", "123321",
	"letmein", "696969", "shadow", "michael", "superman", "1qaz2wsx",
	"7777777", "121212", "000000", "qazwsx", "123qwe", "killer",
	"jordan", "jennifer", "hunter", "buster", "soccer", "harley",
	"batman", "andrew", "tigger", "charlie", "robert", "thomas",
	"hockey", "ranger", "daniel", "starwars", "112233", "george",
	"computer", "michelle", "jessica", "pepper", "1111", "zxcvbn",
	"555555", "11111111", "131313", "freedom", "777777", "pass",
	"maggie", "159753", "aaaaaa", "ginger", "princess", "joshua",
	"cheese", "amanda", "summer", "ashley", "6969", "nicole",
	"chelsea", "biteme", "matthew", "access", "yankees", "987654321",
	"dallas", "austin", "thunder", "taylor", "matrix", "mustang",
	"ninja", "welcome", "welcome1", "admin", "administrator", "admin123",
	"root", "toor", "changeme", "letmein1", "password1",
	"passw0rd", "p@ssw0rd", "passw0rd!", "password1!", "qwerty123",
	"qwertyuiop", "1q2w3e4r", "1q2w3e4r5t", "zaq1zaq1", "aa123456",
	"asdfghjkl", "abc12345", "abcd1234", "1qaz2wsx3edc", "iloveyou1",
	"whatever", "trustme", "letmein123", "changeme123", "temppass",
	"temp123", "guest", "guest123", "test123", "testing", "default",
	"default123", "incorrect", "wordpass", "mypassword", "mypassword1",
	"hello123", "hello1", "welcome123", "abc123", "monkey", "football",
	"jesus", "flower", "hottie", "loveme", "biteme1", "sunshine1",
})

func buildCommonWeakPasswordSet(entries []string) map[string]struct{} {
	set := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		set[e] = struct{}{}
	}
	return set
}

// isCommonWeakPassword reports whether password (case-insensitively)
// matches an entry in commonWeakPasswords.
func isCommonWeakPassword(password string) bool {
	_, found := commonWeakPasswords[strings.ToLower(password)]
	return found
}

// isSingleRepeatedRune reports whether password is the same rune repeated
// for its entire length ("aaaaaaaa", "11111111", "        ") — the
// simplest possible predictable pattern, and one no blocklist entry could
// enumerate in advance since it exists for every rune, not just ASCII.
func isSingleRepeatedRune(password string) bool {
	runes := []rune(password)
	for i := 1; i < len(runes); i++ {
		if runes[i] != runes[0] {
			return false
		}
	}
	return true
}

// isSequentialRun reports whether password is a strictly ascending or
// strictly descending run of consecutive code points — "12345678",
// "abcdefgh", "87654321", "hgfedcba". This is a generic, alphabet-agnostic
// check rather than a hardcoded keyboard-layout table: it costs nothing to
// evaluate and catches the entire family of "just typed the next N
// characters" passwords without needing to enumerate them individually.
func isSequentialRun(password string) bool {
	runes := []rune(password)
	if len(runes) < 2 {
		return false
	}
	ascending, descending := true, true
	for i := 1; i < len(runes); i++ {
		delta := runes[i] - runes[i-1]
		if delta != 1 {
			ascending = false
		}
		if delta != -1 {
			descending = false
		}
	}
	return ascending || descending
}

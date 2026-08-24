package config

import (
	"bufio"
	"os"
	"strings"
)

// loadDotEnv reads key=value pairs from the file at path and sets them as
// environment variables. It follows these rules:
//
//   - Lines starting with '#' (after trimming spaces) are treated as comments and skipped.
//   - Empty lines are skipped.
//   - Each non-empty, non-comment line must contain '='. The key is the part
//     before the first '=' and the value is everything after it.
//   - Keys and values are trimmed of leading/trailing whitespace.
//   - Optional surrounding quotes (" or ') are stripped from values.
//   - Variables already present in the environment are NOT overwritten, so
//     OS-level variables always take precedence over the .env file.
//
// If path does not exist, loadDotEnv returns nil — a missing .env file is
// not an error. Any other I/O error is returned as-is.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip blank lines and comments.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		idx := strings.Index(line, "=")
		if idx < 0 {
			// No '=' found — skip malformed line.
			continue
		}

		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])

		// Strip optional surrounding quotes.
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}

		if key == "" {
			continue
		}

		// OS-level environment takes precedence: only set if not already present.
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}

	return scanner.Err()
}

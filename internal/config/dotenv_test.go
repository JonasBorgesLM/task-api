package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeDotEnv writes content to a new file inside t.TempDir() and returns its path.
func writeDotEnv(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write test .env file: %v", err)
	}
	return path
}

// unsetAfterTest ensures key is removed from the environment once the test finishes.
// Used for keys that loadDotEnv itself sets, as opposed to keys pre-set with t.Setenv
// (which restores automatically).
func unsetAfterTest(t *testing.T, key string) {
	t.Helper()
	t.Cleanup(func() {
		os.Unsetenv(key)
	})
}

func TestLoadDotEnv_MissingFile_ReturnsNil(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.env")

	if err := loadDotEnv(path); err != nil {
		t.Errorf("loadDotEnv() on missing file error = %v, want nil", err)
	}
}

func TestLoadDotEnv_SetsVariable(t *testing.T) {
	unsetAfterTest(t, "TESTENV_BASIC")
	path := writeDotEnv(t, "TESTENV_BASIC=hello\n")

	if err := loadDotEnv(path); err != nil {
		t.Fatalf("loadDotEnv() unexpected error: %v", err)
	}

	if got := os.Getenv("TESTENV_BASIC"); got != "hello" {
		t.Errorf("os.Getenv(TESTENV_BASIC) = %q, want %q", got, "hello")
	}
}

func TestLoadDotEnv_SkipsCommentsAndBlankLines(t *testing.T) {
	unsetAfterTest(t, "TESTENV_AFTER_COMMENT")
	path := writeDotEnv(t, "# this is a comment\n\n   \nTESTENV_AFTER_COMMENT=value\n")

	if err := loadDotEnv(path); err != nil {
		t.Fatalf("loadDotEnv() unexpected error: %v", err)
	}

	if got := os.Getenv("TESTENV_AFTER_COMMENT"); got != "value" {
		t.Errorf("os.Getenv(TESTENV_AFTER_COMMENT) = %q, want %q", got, "value")
	}
}

func TestLoadDotEnv_TrimsWhitespaceAroundKeyAndValue(t *testing.T) {
	unsetAfterTest(t, "TESTENV_TRIMMED")
	path := writeDotEnv(t, "   TESTENV_TRIMMED   =   value with spaces   \n")

	if err := loadDotEnv(path); err != nil {
		t.Fatalf("loadDotEnv() unexpected error: %v", err)
	}

	if got := os.Getenv("TESTENV_TRIMMED"); got != "value with spaces" {
		t.Errorf("os.Getenv(TESTENV_TRIMMED) = %q, want %q", got, "value with spaces")
	}
}

func TestLoadDotEnv_StripsSurroundingQuotes(t *testing.T) {
	unsetAfterTest(t, "TESTENV_DOUBLE_QUOTED")
	unsetAfterTest(t, "TESTENV_SINGLE_QUOTED")
	path := writeDotEnv(t, "TESTENV_DOUBLE_QUOTED=\"double quoted\"\nTESTENV_SINGLE_QUOTED='single quoted'\n")

	if err := loadDotEnv(path); err != nil {
		t.Fatalf("loadDotEnv() unexpected error: %v", err)
	}

	if got := os.Getenv("TESTENV_DOUBLE_QUOTED"); got != "double quoted" {
		t.Errorf("os.Getenv(TESTENV_DOUBLE_QUOTED) = %q, want %q", got, "double quoted")
	}
	if got := os.Getenv("TESTENV_SINGLE_QUOTED"); got != "single quoted" {
		t.Errorf("os.Getenv(TESTENV_SINGLE_QUOTED) = %q, want %q", got, "single quoted")
	}
}

func TestLoadDotEnv_SkipsMalformedLineWithoutEquals(t *testing.T) {
	unsetAfterTest(t, "TESTENV_AFTER_MALFORMED")
	path := writeDotEnv(t, "THIS_LINE_HAS_NO_EQUALS_SIGN\nTESTENV_AFTER_MALFORMED=value\n")

	if err := loadDotEnv(path); err != nil {
		t.Fatalf("loadDotEnv() unexpected error: %v", err)
	}

	if _, exists := os.LookupEnv("THIS_LINE_HAS_NO_EQUALS_SIGN"); exists {
		t.Error("loadDotEnv() must not set a variable from a malformed line")
	}
	if got := os.Getenv("TESTENV_AFTER_MALFORMED"); got != "value" {
		t.Errorf("os.Getenv(TESTENV_AFTER_MALFORMED) = %q, want %q", got, "value")
	}
}

func TestLoadDotEnv_SkipsEmptyKey(t *testing.T) {
	path := writeDotEnv(t, "=value-with-no-key\n")

	if err := loadDotEnv(path); err != nil {
		t.Fatalf("loadDotEnv() unexpected error: %v", err)
	}
	// Nothing to assert by name since the key is empty; the test's purpose is
	// to prove loadDotEnv doesn't panic or error on this input.
}

func TestLoadDotEnv_DoesNotOverwriteExistingEnv(t *testing.T) {
	t.Setenv("TESTENV_PRECEDENCE", "from-os-env")
	path := writeDotEnv(t, "TESTENV_PRECEDENCE=from-dotenv-file\n")

	if err := loadDotEnv(path); err != nil {
		t.Fatalf("loadDotEnv() unexpected error: %v", err)
	}

	if got := os.Getenv("TESTENV_PRECEDENCE"); got != "from-os-env" {
		t.Errorf("os.Getenv(TESTENV_PRECEDENCE) = %q, want %q (OS env must take precedence)", got, "from-os-env")
	}
}

func TestLoadDotEnv_ReadErrorOnDirectory(t *testing.T) {
	dir := t.TempDir()

	err := loadDotEnv(dir)
	if err == nil {
		t.Fatal("loadDotEnv() on a directory path: expected error, got nil")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Errorf("loadDotEnv() error = %v, want a non-NotExist I/O error", err)
	}
}

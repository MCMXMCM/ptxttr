package config

import (
	"testing"
	"time"
)

func TestDurationEnvFallsBackForInvalidValues(t *testing.T) {
	t.Setenv("PTXT_TEST_TIMEOUT", "not-a-number")
	if got := durationEnv("PTXT_TEST_TIMEOUT", 1500*time.Millisecond); got != 1500*time.Millisecond {
		t.Fatalf("durationEnv invalid = %s, want fallback", got)
	}
	t.Setenv("PTXT_TEST_TIMEOUT", "0")
	if got := durationEnv("PTXT_TEST_TIMEOUT", 1500*time.Millisecond); got != 1500*time.Millisecond {
		t.Fatalf("durationEnv zero = %s, want fallback", got)
	}
}

func TestDurationEnvParsesMilliseconds(t *testing.T) {
	t.Setenv("PTXT_TEST_TIMEOUT", "2500")
	if got := durationEnv("PTXT_TEST_TIMEOUT", time.Second); got != 2500*time.Millisecond {
		t.Fatalf("durationEnv = %s, want 2500ms", got)
	}
}

func TestBoolEnvParsesKnownValues(t *testing.T) {
	t.Setenv("PTXT_TEST_BOOL", "yes")
	if !boolEnv("PTXT_TEST_BOOL", false) {
		t.Fatal("boolEnv yes = false, want true")
	}
	t.Setenv("PTXT_TEST_BOOL", "off")
	if boolEnv("PTXT_TEST_BOOL", true) {
		t.Fatal("boolEnv off = true, want false")
	}
	t.Setenv("PTXT_TEST_BOOL", "maybe")
	if !boolEnv("PTXT_TEST_BOOL", true) {
		t.Fatal("boolEnv unknown did not use fallback")
	}
}

func TestIntEnvFallsBackForInvalidValues(t *testing.T) {
	t.Setenv("PTXT_TEST_INT", "not-a-number")
	if got := intEnv("PTXT_TEST_INT", 42); got != 42 {
		t.Fatalf("intEnv invalid = %d, want fallback", got)
	}
	t.Setenv("PTXT_TEST_INT", "123")
	if got := intEnv("PTXT_TEST_INT", 42); got != 123 {
		t.Fatalf("intEnv = %d, want 123", got)
	}
}

func TestFloatEnvFallsBackForInvalidValues(t *testing.T) {
	t.Setenv("PTXT_TEST_FLOAT", "not-a-number")
	if got := floatEnv("PTXT_TEST_FLOAT", 1.5); got != 1.5 {
		t.Fatalf("floatEnv invalid = %f, want fallback", got)
	}
	t.Setenv("PTXT_TEST_FLOAT", "2.25")
	if got := floatEnv("PTXT_TEST_FLOAT", 1.5); got != 2.25 {
		t.Fatalf("floatEnv = %f, want 2.25", got)
	}
}

func TestDurationEnvDurationParsesStrings(t *testing.T) {
	t.Setenv("PTXT_TEST_WINDOW", "2h")
	if got := durationEnvDuration("PTXT_TEST_WINDOW", time.Hour); got != 2*time.Hour {
		t.Fatalf("durationEnvDuration = %s, want 2h", got)
	}
	t.Setenv("PTXT_TEST_WINDOW", "bad")
	if got := durationEnvDuration("PTXT_TEST_WINDOW", time.Hour); got != time.Hour {
		t.Fatalf("durationEnvDuration invalid = %s, want fallback", got)
	}
}

func TestSeedAuthorNoteLookbackEnv(t *testing.T) {
	t.Setenv("PTXT_SEED_LB", "720h")
	if got := seedAuthorNoteLookbackEnv("PTXT_SEED_LB", 24*time.Hour); got != 720*time.Hour {
		t.Fatalf("lookback = %s, want 720h", got)
	}
	t.Setenv("PTXT_SEED_LB", "0")
	if got := seedAuthorNoteLookbackEnv("PTXT_SEED_LB", 24*time.Hour); got != 0 {
		t.Fatalf("lookback disabled = %s, want 0", got)
	}
	t.Setenv("PTXT_SEED_LB", "")
	if got := seedAuthorNoteLookbackEnv("PTXT_SEED_LB", 24*time.Hour); got != 24*time.Hour {
		t.Fatalf("lookback empty = %s, want fallback", got)
	}
}

func TestIngestVerifyParallelEnv(t *testing.T) {
	t.Setenv("PTXT_INGEST_VERIFY_PARALLEL", "")
	if got := ingestVerifyParallelEnv(); got != 0 {
		t.Fatalf("empty = %d, want 0", got)
	}
	t.Setenv("PTXT_INGEST_VERIFY_PARALLEL", "4")
	if got := ingestVerifyParallelEnv(); got != 4 {
		t.Fatalf("4 = %d", got)
	}
	t.Setenv("PTXT_INGEST_VERIFY_PARALLEL", "99")
	if got := ingestVerifyParallelEnv(); got != 32 {
		t.Fatalf("cap = %d, want 32", got)
	}
	t.Setenv("PTXT_INGEST_VERIFY_PARALLEL", "-1")
	if got := ingestVerifyParallelEnv(); got != 0 {
		t.Fatalf("negative = %d, want 0", got)
	}
}

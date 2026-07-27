package rollout

import (
	"bytes"
	"io/fs"
	"strings"
	"testing"
)

func fakeGetenv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

// -- Resolve: the two-variable state machine --

func TestResolve_NoEnvSet_Disabled(t *testing.T) {
	if got := Resolve(fakeGetenv(nil)); got != Disabled {
		t.Fatalf("Resolve(no env) = %v, want Disabled", got)
	}
}

func TestResolve_EnforceWithoutAttestation_SelfApplicationRisk(t *testing.T) {
	got := Resolve(fakeGetenv(map[string]string{EnvVar: "1"}))
	if got != SelfApplicationRisk {
		t.Fatalf("Resolve(enforce only) = %v, want SelfApplicationRisk", got)
	}
}

func TestResolve_AttestationWithoutEnforce_StillDisabled(t *testing.T) {
	got := Resolve(fakeGetenv(map[string]string{ValidatedEnvVar: "1"}))
	if got != Disabled {
		t.Fatalf("Resolve(attestation only) = %v, want Disabled", got)
	}
}

func TestResolve_BothSet_Enabled(t *testing.T) {
	got := Resolve(fakeGetenv(map[string]string{EnvVar: "1", ValidatedEnvVar: "1"}))
	if got != Enabled {
		t.Fatalf("Resolve(both) = %v, want Enabled", got)
	}
}

func TestResolve_LooseTruthyValuesDoNotOptIn(t *testing.T) {
	got := Resolve(fakeGetenv(map[string]string{EnvVar: "true", ValidatedEnvVar: "yes"}))
	if got != Disabled {
		t.Fatalf("Resolve(loose truthy values) = %v, want Disabled (only exact \"1\" opts in)", got)
	}
}

// -- Run: fake filesystem fixtures --

func primaryLstat(name string) (fs.FileInfo, error) {
	if name == "/repo/.git" {
		return dirInfo{}, nil
	}
	return nil, fs.ErrNotExist
}

func primaryReadFile(string) ([]byte, error) { return nil, fs.ErrNotExist }

type dirInfo struct{ fs.FileInfo }

func (dirInfo) IsDir() bool { return true }

// -- Run: Disabled never enforces, but observes --

func TestRun_Disabled_NeverDeniesButLogsTheObservedDeny(t *testing.T) {
	in := strings.NewReader(`{"tool_name":"Write","tool_input":{"file_path":"/repo/a.go"}}`)
	var out, errOut bytes.Buffer

	code := Run(Disabled, in, &out, &errOut, primaryLstat, primaryReadFile)

	if code != 0 || out.Len() != 0 {
		t.Fatalf("Run(Disabled) = code=%d stdout=%q, want a silent allow -- disabled must never enforce", code, out.String())
	}
	if !strings.Contains(errOut.String(), "rollout disabled") {
		t.Errorf("Run(Disabled) stderr = %q, want an observed-deny note for a call the real gate would have denied", errOut.String())
	}
}

func TestRun_Disabled_TotallySilentWhenTheRealGateWouldAllow(t *testing.T) {
	in := strings.NewReader(`{"tool_name":"Bash","cwd":"/repo","tool_input":{"command":"git status"}}`)
	var out, errOut bytes.Buffer

	code := Run(Disabled, in, &out, &errOut, primaryLstat, primaryReadFile)

	if code != 0 || out.Len() != 0 || errOut.Len() != 0 {
		t.Fatalf("Run(Disabled) on an allow = code=%d stdout=%q stderr=%q, want total silence", code, out.String(), errOut.String())
	}
}

// -- Run: Enabled delegates straight to detect.Run --

func TestRun_Enabled_DelegatesToDetectAndEnforces(t *testing.T) {
	in := strings.NewReader(`{"tool_name":"Write","tool_input":{"file_path":"/repo/a.go"}}`)
	var out, errOut bytes.Buffer

	code := Run(Enabled, in, &out, &errOut, primaryLstat, primaryReadFile)

	if code != 0 || !strings.Contains(out.String(), `"deny"`) {
		t.Fatalf("Run(Enabled) = code=%d stdout=%q, want the real deny response enforced on stdout", code, out.String())
	}
}

// -- Run: SelfApplicationRisk never enforces and forces a pause --

func TestRun_SelfApplicationRisk_NeverDeniesAndForcesADistinctExitCode(t *testing.T) {
	in := strings.NewReader(`{"tool_name":"Write","tool_input":{"file_path":"/repo/a.go"}}`)
	var out, errOut bytes.Buffer

	code := Run(SelfApplicationRisk, in, &out, &errOut, primaryLstat, primaryReadFile)

	if out.Len() != 0 {
		t.Fatalf("Run(SelfApplicationRisk) stdout = %q, want nothing emitted -- the call must still proceed", out.String())
	}
	if code != ForcedPauseExitCode {
		t.Fatalf("Run(SelfApplicationRisk) exit code = %d, want %d", code, ForcedPauseExitCode)
	}
	if !strings.Contains(errOut.String(), ValidatedEnvVar) {
		t.Errorf("Run(SelfApplicationRisk) stderr = %q, want it to name the missing attestation", errOut.String())
	}
}

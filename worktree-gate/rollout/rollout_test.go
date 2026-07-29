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

// -- Resolve: enforcement defaults on, "0" is the only opt-out --

func TestResolve_NoEnvSet_Enabled(t *testing.T) {
	if got := Resolve(fakeGetenv(nil)); got != Enabled {
		t.Fatalf("Resolve(no env) = %v, want Enabled (enforcement is on by default)", got)
	}
}

func TestResolve_ExplicitZero_Disabled(t *testing.T) {
	got := Resolve(fakeGetenv(map[string]string{EnvVar: "0"}))
	if got != Disabled {
		t.Fatalf("Resolve(%s=0) = %v, want Disabled", EnvVar, got)
	}
}

func TestResolve_ExplicitOne_StillEnabled(t *testing.T) {
	got := Resolve(fakeGetenv(map[string]string{EnvVar: "1"}))
	if got != Enabled {
		t.Fatalf("Resolve(%s=1) = %v, want Enabled", EnvVar, got)
	}
}

func TestResolve_LooseFalsyValuesDoNotOptOut(t *testing.T) {
	got := Resolve(fakeGetenv(map[string]string{EnvVar: "false"}))
	if got != Enabled {
		t.Fatalf("Resolve(%s=false) = %v, want Enabled (only exact \"0\" opts out)", EnvVar, got)
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

	code := Run(Disabled, in, &out, &errOut, primaryLstat, primaryReadFile, fakeGetenv(nil))

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

	code := Run(Disabled, in, &out, &errOut, primaryLstat, primaryReadFile, fakeGetenv(nil))

	if code != 0 || out.Len() != 0 || errOut.Len() != 0 {
		t.Fatalf("Run(Disabled) on an allow = code=%d stdout=%q stderr=%q, want total silence", code, out.String(), errOut.String())
	}
}

// -- Run: Enabled delegates straight to detect.Run --

func TestRun_Enabled_DelegatesToDetectAndEnforces(t *testing.T) {
	in := strings.NewReader(`{"tool_name":"Write","tool_input":{"file_path":"/repo/a.go"}}`)
	var out, errOut bytes.Buffer

	code := Run(Enabled, in, &out, &errOut, primaryLstat, primaryReadFile, fakeGetenv(nil))

	if code != 0 || !strings.Contains(out.String(), `"deny"`) {
		t.Fatalf("Run(Enabled) = code=%d stdout=%q, want the real deny response enforced on stdout", code, out.String())
	}
}

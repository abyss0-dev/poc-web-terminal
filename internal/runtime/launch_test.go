package runtime

import (
	"strings"
	"testing"
)

func argsString(a []string) string { return " " + strings.Join(a, " ") + " " }

func hasFlagValue(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func testTC() TargetConfig {
	return TargetConfig{
		ID: "vm1", Image: "vm/overlay-vm1.qcow2", Seed: "vm/seed-vm1.img",
		SSHPort: 2222, Memory: "1024",
	}
}

func TestQEMUArgsWithKVMUsesHostCPU(t *testing.T) {
	args := qemuArgs(testTC(), true)
	if !contains(args, "-enable-kvm") {
		t.Fatalf("kvm args missing -enable-kvm: %v", args)
	}
	if !hasFlagValue(args, "-cpu", "host") {
		t.Fatalf("kvm args missing -cpu host: %v", args)
	}
}

func TestQEMUArgsWithoutKVMOmitsHostCPU(t *testing.T) {
	args := qemuArgs(testTC(), false)
	if contains(args, "-enable-kvm") {
		t.Fatalf("tcg args must not enable kvm: %v", args)
	}
	if hasFlagValue(args, "-cpu", "host") {
		t.Fatalf("tcg args must not request -cpu host (requires KVM): %v", args)
	}
	if !strings.Contains(argsString(args), "accel=tcg") {
		t.Fatalf("tcg args should select tcg accel: %v", args)
	}
}

func TestQEMUArgsCommonShape(t *testing.T) {
	args := qemuArgs(testTC(), true)
	s := argsString(args)
	for _, want := range []string{
		"-m 1024",
		"file=vm/overlay-vm1.qcow2",
		"file=vm/seed-vm1.img",
		"hostfwd=tcp:127.0.0.1:2222-:22",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("args missing %q: %v", want, args)
		}
	}
}

func TestQEMUArgsDefaultMemory(t *testing.T) {
	tc := testTC()
	tc.Memory = ""
	if !hasFlagValue(qemuArgs(tc, false), "-m", "1024") {
		t.Fatal("empty memory should default to 1024")
	}
}

func TestKVMAvailableReturnsWithoutPanic(t *testing.T) {
	// Environment-dependent; we only assert it is callable and consistent with
	// the internal helper used by the accelerator decision.
	if KVMAvailable() != kvmAvailable() {
		t.Fatal("KVMAvailable and kvmAvailable disagree")
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

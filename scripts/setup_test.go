package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupLocal(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "scripts")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("setup-local.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "setup-local.sh"), script, 0700); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	if err = os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	mock := `#!/usr/bin/env bash
set -eu
if [[ $1 == -install ]]; then exit 0; fi
[[ $1 == -cert-file && $3 == -key-file ]]
printf cert > "$2"
printf key > "$4"
`
	if err = os.WriteFile(filepath.Join(bin, "mkcert"), []byte(mock), 0700); err != nil {
		t.Fatal(err)
	}
	hosts := filepath.Join(root, "hosts")
	if err = os.WriteFile(hosts, []byte("127.0.0.1 localhost\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		t.Helper()
		c := exec.Command("bash", append([]string{filepath.Join(dir, "setup-local.sh"), "--hosts-file", hosts}, args...)...)
		c.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
		out, e := c.CombinedOutput()
		return string(out), e
	}
	if out, e := run("hello-world", "other", "other"); e != nil {
		t.Fatal(out, e)
	}
	if out, e := run("hello-world", "other"); e != nil {
		t.Fatal(out, e)
	}
	b, err := os.ReadFile(hosts)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pmq.dev", "hello-world.pmq.dev", "other.pmq.dev"} {
		count := 0
		for _, line := range strings.Split(string(b), "\n") {
			fields := strings.Fields(line)
			if len(fields) > 1 && fields[1] == name {
				count++
			}
		}
		if count != 1 {
			t.Errorf("%s appears %d times: %s", name, count, b)
		}
	}
	key := filepath.Join(root, ".local/certs/pmq.dev-key.pem")
	st, err := os.Stat(key)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("key permissions", st, err)
	}
	if _, e := run("../bad"); e == nil {
		t.Fatal("invalid name accepted")
	}
	if err = os.WriteFile(hosts, []byte("192.0.2.10 hello-world.pmq.dev\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, e := run(); e == nil || !strings.Contains(out, "Conflicting") {
		t.Fatal("conflict accepted", out, e)
	}
}

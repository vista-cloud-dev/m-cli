package driver

import (
	"errors"
	"testing"
)

func deps(env map[string]string, files map[string]bool, exeDir string, path map[string]string) LocateDeps {
	return LocateDeps{
		Getenv: func(k string) string { return env[k] },
		IsFile: func(p string) bool { return files[p] },
		ExeDir: func() (string, error) { return exeDir, nil },
		LookPath: func(b string) (string, error) {
			if p, ok := path[b]; ok {
				return p, nil
			}
			return "", errors.New("not on PATH")
		},
	}
}

func TestLocate_EnvWins(t *testing.T) {
	got, err := Locate("iris", deps(map[string]string{"M_IRIS_BIN": "/custom/m-iris"}, nil, "/x", nil))
	if err != nil || got != "/custom/m-iris" {
		t.Fatalf("got %q, %v; want /custom/m-iris", got, err)
	}
}

func TestLocate_NextToExe(t *testing.T) {
	got, err := Locate("ydb", deps(nil, map[string]bool{"/opt/bin/m-ydb": true}, "/opt/bin", nil))
	if err != nil || got != "/opt/bin/m-ydb" {
		t.Fatalf("got %q, %v; want /opt/bin/m-ydb", got, err)
	}
}

func TestLocate_SiblingDist(t *testing.T) {
	// `m` at /ws/m-cli/dist/m → sibling driver at /ws/m-ydb/dist/m-ydb (the
	// ../.. is cleaned by filepath.Join).
	got, err := Locate("ydb", deps(nil, map[string]bool{"/ws/m-ydb/dist/m-ydb": true}, "/ws/m-cli/dist", nil))
	if err != nil || got != "/ws/m-ydb/dist/m-ydb" {
		t.Fatalf("got %q, %v; want /ws/m-ydb/dist/m-ydb", got, err)
	}
}

func TestLocate_PathFallback(t *testing.T) {
	got, err := Locate("iris", deps(nil, nil, "/x", map[string]string{"m-iris": "/usr/bin/m-iris"}))
	if err != nil || got != "/usr/bin/m-iris" {
		t.Fatalf("got %q, %v; want /usr/bin/m-iris", got, err)
	}
}

func TestLocate_NotFound(t *testing.T) {
	if _, err := Locate("ydb", deps(nil, nil, "/x", nil)); err == nil {
		t.Error("want an error when the driver binary is nowhere")
	}
}

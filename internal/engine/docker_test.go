package engine

import "testing"

func TestDockerEnvPrefix(t *testing.T) {
	cases := []struct {
		name     string
		stageDir string
		want     string
	}{
		{"no stage dir → no prefix", "", ""},
		{
			// The staged dir is prepended to the routine path, and the base falls
			// back to $gtmroutines when $ydb_routines is unset — a GT.M-configured
			// VistA (e.g. vehu) sets gtmroutines, not ydb_routines, and ydb_routines
			// once set overrides gtmroutines, so it must carry the resident base or
			// the engine's own routines (XPAR, FileMan, …) vanish.
			"stage dir prepends + falls back to gtmroutines",
			"/m-work",
			`export ydb_routines="/m-work ${ydb_routines:-$gtmroutines}"; `,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dockerEnvPrefix(tc.stageDir); got != tc.want {
				t.Errorf("dockerEnvPrefix(%q) = %q, want %q", tc.stageDir, got, tc.want)
			}
		})
	}
}

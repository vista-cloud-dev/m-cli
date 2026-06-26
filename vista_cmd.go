package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/vista-cloud-dev/clikit"
	"github.com/vista-cloud-dev/m-cli/internal/engine"
	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
)

// vistaCmd is the driver-backed engine surface: it reaches a live VistA (or any
// engine) by invoking the m-<engine> driver binary over the neutral contract,
// rather than the in-process YDB/IRIS engines. This is the m-cli side of VSL
// T0.1 — "the runner opens a session on each engine and runs W $ZV (both
// reachable)" — realized via the conformance-gated drivers (m-ydb, m-iris).
type vistaCmd struct {
	Status vistaStatusCmd `cmd:"" help:"Probe a live VistA via its driver: running / healthy / version (reachability + W $ZV gate)."`
	Exec   vistaExecCmd   `cmd:"" help:"Evaluate one M command on a live VistA via its driver."`
}

// vistaConn selects which engine driver to drive and over which transport. The
// connection itself (host/container/base-url, credentials) is read by the driver
// from its M_<ENGINE>_* environment, so it never appears here.
type vistaConn struct {
	Engine    string `help:"Engine to reach: ydb or iris." enum:"ydb,iris" required:""`
	Transport string `help:"Driver transport: local | docker | remote." enum:"local,docker,remote" default:"remote"`
}

// build resolves the driver binary (driver-contract §4) and returns the
// driver-backed engine.
func (v vistaConn) build() (*engine.VistaEngine, error) {
	bin, err := mdriver.Locate(v.Engine, mdriver.DefaultLocateDeps())
	if err != nil {
		return nil, err
	}
	cl := mdriver.NewClient(bin, v.Engine, v.Transport, nil, nil)
	return engine.NewVista(engine.Kind(v.Engine), cl), nil
}

type vistaStatusCmd struct {
	vistaConn
}

func (c *vistaStatusCmd) Run(cc *clikit.Context) error {
	eng, err := c.build()
	if err != nil {
		return clikit.Fail(clikit.ExitRefused, "NO_DRIVER", err.Error(),
			"build the m-"+c.Engine+" driver (make build) or set M_"+strings.ToUpper(c.Engine)+"_BIN")
	}
	st, err := eng.Probe(context.Background())
	if err != nil {
		return clikit.Fail(clikit.ExitRefused, "UNREACHABLE", err.Error(),
			"check the driver connection (M_"+strings.ToUpper(c.Engine)+"_* env)")
	}
	return cc.Result(st, func() {
		cc.Title(fmt.Sprintf("vista %s — %s", c.Engine, c.Transport))
		cc.KV(
			[2]string{"running", fmt.Sprint(st.Running)},
			[2]string{"healthy", fmt.Sprint(st.Healthy)},
			[2]string{"version", st.Version},
		)
	})
}

type vistaExecCmd struct {
	vistaConn
	Command []string `arg:"" help:"M command to evaluate (quote it as one shell arg)."`
}

type vistaExecResult struct {
	Stdout string `json:"stdout"`
	Status int    `json:"status"`
	Stderr string `json:"stderr,omitempty"`
}

func (c *vistaExecCmd) Run(cc *clikit.Context) error {
	eng, err := c.build()
	if err != nil {
		return clikit.Fail(clikit.ExitRefused, "NO_DRIVER", err.Error(),
			"build the m-"+c.Engine+" driver (make build) or set M_"+strings.ToUpper(c.Engine)+"_BIN")
	}
	res, err := eng.RunXCmd(context.Background(), strings.Join(c.Command, " "))
	if err != nil {
		return clikit.Fail(clikit.ExitRuntime, "EXEC", err.Error(), "")
	}
	return cc.Result(vistaExecResult{Stdout: res.Stdout, Status: res.ExitCode, Stderr: res.Stderr}, func() {
		if res.Stdout != "" {
			fmt.Fprintln(cc.Stdout, res.Stdout)
		}
		if res.Stderr != "" {
			fmt.Fprintln(cc.Stdout, cc.Faint(res.Stderr))
		}
		fmt.Fprintln(cc.Stdout, cc.Faint(fmt.Sprintf("status %d", res.ExitCode)))
	})
}

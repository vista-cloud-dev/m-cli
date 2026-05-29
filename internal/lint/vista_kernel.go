package lint

// VistA Kernel auto-defined locals — the M-MOD-024 allowlist (ported from the
// Python tool's _vista_kernel.py). The Kernel initializes these process-scoped
// locals at session/routine entry in another routine that the static
// reaching-defs analysis cannot see, so a read of one is a guaranteed false
// positive in any VistA context.
//
// Faithful to the Python tool, this allowlist is OFF by default and gated behind
// [lint.vista] kernel_locals: non-VA code shouldn't get a free pass on these
// names. M-MOD-024 receives this map only when the config opts in with
// kernel_locals = "default" (see OptionsFromConfig / DefaultKernelLocals); with
// no config or an explicit name list, M-MOD-024 stays strict / uses that list.
var kernelAutoDefined = func() map[string]bool {
	names := []string{
		// The universal field separator — Kernel sets it to $C(94) at every
		// routine entry. By far the most common false-positive source.
		"U",
		// Device-handling locals (Kernel session).
		"IO", "IOM", "IOSL", "IOST", "IOST(0)", "IOF", "IOXY", "IOBS", "IOTM", "IOTBL", "IOSC",
		// Date/time (Kernel + FileMan, set at sign-on).
		"DT", "DTIME",
		// User identity (Kernel sign-on).
		"DUZ",
		// Environment / namespace.
		"%UCI", "%H", "%XQDIC", "%ZIS", "%ZTOS", "%ZTSCH", "%ZTLOAD",
		// TaskMan task variables.
		"ZTQUEUED", "ZTSK", "ZTREQ", "ZTSTOP", "ZTIO", "ZTDESC", "ZTDTH", "ZTRTN", "ZTSAVE", "ZTUCI", "ZTVOL",
		// XQ option/menu system.
		"XQXFLG", "XQY", "XQY0", "XQT0", "XQT1", "XQOPTKEY", "XQOPT",
		// XM (MailMan).
		"XMC", "XMINSTR", "XMDUZ", "XMSUB", "XMTEXT", "XMY", "XMZ",
	}
	// ^DIR / ^DIC write back into Y / X / DTOUT / DUOUT only post-call, so they
	// are deliberately omitted — suppressing them would mask real use-before-call bugs.
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}()

// DefaultKernelLocals returns a copy of the built-in VistA Kernel auto-defined
// allowlist — the set [lint.vista] kernel_locals = "default" opts in to.
func DefaultKernelLocals() map[string]bool {
	m := make(map[string]bool, len(kernelAutoDefined))
	for k := range kernelAutoDefined {
		m[k] = true
	}
	return m
}

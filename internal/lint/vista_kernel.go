package lint

// VistA Kernel auto-defined locals — the M-MOD-024 allowlist (ported from the
// Python tool's _vista_kernel.py). The Kernel initializes these process-scoped
// locals at session/routine entry in another routine that the static
// reaching-defs analysis cannot see, so a read of one is a guaranteed false
// positive in any VistA context.
//
// Unlike the Python tool — which gates this behind [lint.vista] kernel_locals
// because modern non-VA code shouldn't get a free pass on these names — the Go
// tool applies the allowlist unconditionally: there is no lint config plumbing
// yet, and these ~50 names are so VistA-specific that an undefined read of one
// in a non-VA project is vanishingly rare. Revisit when [lint.vista] lands.
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

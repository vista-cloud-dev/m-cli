package lint

// trustedRoutines is the VistA-trusted routine allowlist for M-XINDX-007 (call
// to undefined routine) — universally-present FileMan / Kernel / MailMan / device
// APIs that live outside the source-controlled application packages (so they're
// absent from a workspace index of one package). Ported verbatim from the Python
// tool's lint/_vista_trusted.py. Opt in via [lint.vista] trusted_routines =
// "default"; without it M-XINDX-007 is strict (any unknown routine flagged).
// Comparison is case-insensitive and ignores a leading ^.
var trustedRoutines = []string{
	"%DT", "%DTC", "%DTCH", "%RCR",
	"DI", "DIA", "DIB", "DIBT", "DIC", "DIC1", "DICATR",
	"DICATR1", "DICATR2", "DICN", "DICR", "DICRC", "DICRC1",
	"DICRW", "DICUIX", "DICUIX1", "DICUIX2",
	"DIE", "DIE1", "DIE2", "DIEZ", "DIE0", "DIET", "DIEM",
	"DIK", "DIK1", "DIKC", "DIKZ", "DIKZ1",
	"DILF", "DILFD", "DIM", "DIM1", "DIM2",
	"DIP", "DIP1", "DIPDR", "DIPM", "DIQ", "DIQ1", "DIR",
	"DIR0", "DIST", "DIU", "DIU1", "DIU2", "DIWP",
	"%ZIS", "%ZISC", "%ZISH", "%ZISL", "%ZISP", "%ZISS", "%ZISTCP",
	"%ZTBKC", "%ZTLOAD", "%ZTLOAD1", "%ZTLOAD2", "%ZTM", "%ZTMG",
	"%ZTPP", "%ZTSCH", "%ZTUL", "%ZTER", "%ZTERH",
	"%ZOSV", "%ZOSF", "%ZOSV1",
	"XLFDT", "XLFDT1", "XLFDT2", "XLFSTR", "XLFNAME", "XLFNAME2",
	"XLFNUM", "XLFMTH", "XLFCRC", "XLFSHAN", "XLFCRC1", "XLFHEX",
	"XLFUTL",
	"XUS", "XUS1", "XUSCLEAN", "XUSER", "XUSERAU", "XUSPSET",
	"XUSESIG", "XUSRB", "XUSRB1", "XUSRB2",
	"XPDIQ", "XPDUTL", "XPDUTL1", "XPDIQ1", "XPDIA", "XPDIE",
	"XPDIJ", "XPDR", "XPDRSUM", "XPDIE1",
	"XQ12", "XQAL", "XQALERT", "XQALSURO", "XQH", "XQOR",
	"XQOR1", "XQDATE", "XQDIC", "XQOPTKEY",
	"XMA", "XMA1", "XMA1A", "XMA1B", "XMA2", "XMA21", "XMA3",
	"XMA4", "XMA8", "XMA9", "XMAA", "XMAB", "XMAH", "XMAS",
	"XMC", "XMC1", "XMD", "XMDIQ", "XMG", "XMG1", "XMJBD",
	"XMJBM", "XMJMF", "XMJMG", "XMJMH", "XMJMI", "XMS",
	"XMS1", "XMSE", "XMTRD", "XMV", "XMVUP", "XMX", "XMXAPI",
	"XMXAPIB", "XMXAPIG", "XMXAPIS", "XMXAPIU", "XMY",
	"XBLM", "XBNEW", "XBHANDLR", "XBHCLE",
	"DIWE", "DIWE1", "DIWE2", "DIWEDT",
	"%G", "%GL", "%GS", "%GE", "%GD",
	"%DH", "%DTC1",
}

// DefaultTrustedRoutines returns the built-in VistA trusted-routine allowlist as
// an upper-cased set ([lint.vista] trusted_routines = "default").
func DefaultTrustedRoutines() map[string]bool {
	m := make(map[string]bool, len(trustedRoutines))
	for _, r := range trustedRoutines {
		m[r] = true // already upper-cased, no leading ^
	}
	return m
}

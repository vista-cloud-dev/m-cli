package lint

// Standard command / intrinsic-special-variable / intrinsic-function sets used
// by the XINDEX portability rules (M-XINDX-002 non-standard Z command,
// M-XINDX-028 non-standard $Z special variable, M-XINDX-031 non-standard $Z
// function). Ported verbatim from the Python tool's lint/_keywords.py fallback
// sets (the canonical source is m-standard's integrated TSVs; the Go port
// bundles the fallback, which is what the Python tool itself uses when
// m-standard is not on disk). A $Z* / Z* token NOT in its set is non-standard.

func toSet(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

var standardCommands = toSet([]string{
	"B", "BREAK", "C", "CLOSE", "D", "DO", "E", "ELSE", "F", "FOR", "G", "GOTO",
	"H", "HALT", "HANG", "I", "IF", "J", "JOB", "K", "KILL", "L", "LOCK",
	"M", "MERGE", "N", "NEW", "O", "OPEN", "Q", "QUIT", "R", "READ", "S", "SET",
	"TC", "TCOMMIT", "TRE", "TRESTART", "TRO", "TROLLBACK", "TS", "TSTART",
	"U", "USE", "V", "VIEW", "W", "WRITE", "X", "XECUTE",
})

var standardISVs = toSet([]string{
	"$D", "$DEVICE", "$EC", "$ECODE", "$ES", "$ESTACK", "$ET", "$ETRAP",
	"$H", "$HOROLOG", "$I", "$IO", "$J", "$JOB", "$K", "$KEY", "$P", "$PRINCIPAL",
	"$Q", "$QUIT", "$R", "$REFERENCE", "$ST", "$STACK", "$S", "$STORAGE",
	"$SY", "$SYSTEM", "$T", "$TEST", "$TL", "$TLEVEL", "$TR", "$TRESTART",
	"$X", "$Y", "$ZA", "$ZB", "$ZC", "$ZD", "$ZE", "$ZG", "$ZH", "$ZI", "$ZJ",
	"$ZL", "$ZN", "$ZO", "$ZP", "$ZR", "$ZS", "$ZT", "$ZU", "$ZV", "$ZEOF",
	"$ZERROR", "$ZHOROLOG", "$ZIO", "$ZJOB", "$ZMODE", "$ZTRAP", "$ZVERSION",
	"$ZPOSITION",
})

var standardFunctions = toSet([]string{
	"$A", "$ASCII", "$C", "$CHAR", "$D", "$DATA", "$E", "$EXTRACT", "$F", "$FIND",
	"$FNUMBER", "$G", "$GET", "$I", "$INCREMENT", "$J", "$JUSTIFY", "$L", "$LENGTH",
	"$LISTBUILD", "$LISTGET", "$LIST", "$LISTDATA", "$LISTFIND", "$LISTLENGTH",
	"$LISTNEXT", "$LISTSAME", "$LISTVALID", "$N", "$NA", "$NAME", "$NEXT",
	"$O", "$ORDER", "$P", "$PIECE", "$Q", "$QLENGTH", "$QSUBSCRIPT", "$QUERY",
	"$R", "$RANDOM", "$REVERSE", "$S", "$SELECT", "$ST", "$STACK", "$T", "$TEXT",
	"$TRANSLATE", "$V", "$VIEW",
	"$ZA", "$ZB", "$ZC", "$ZCONVERT", "$ZD", "$ZDATE", "$ZDH", "$ZDT", "$ZDTH",
	"$ZF", "$ZH", "$ZHEX", "$ZN", "$ZP", "$ZPREVIOUS", "$ZS", "$ZSEARCH",
	"$ZSTRIP", "$ZT", "$ZTH", "$ZTIME", "$ZTRNLNM", "$ZU", "$ZW", "$ZWIDTH",
	"$ZWRITE",
})

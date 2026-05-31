PARITYFAILTST  ; Parity fixture: 1 pass + 1 fail.
        ; Proves the resident no-halt path frames a FAILING suite identically to
        ; the file-side per-process runner (where report^STDASSERT halts). Kept
        ; out of m-stdlib's own tests/ so `make test` never runs it standalone.
        new pass,fail
        do start^STDASSERT(.pass,.fail)
        do tParityPasses(.pass,.fail)
        do tParityFails(.pass,.fail)
        do report^STDASSERT(pass,fail)
        quit
        ;
tParityPasses(pass,fail)        ;@TEST "an assertion that passes"
        do eq^STDASSERT(.pass,.fail,1,1,"one is one")
        quit
        ;
tParityFails(pass,fail) ;@TEST "an assertion that fails on purpose"
        do eq^STDASSERT(.pass,.fail,1,2,"one is two (deliberate)")
        quit

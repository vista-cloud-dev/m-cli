SAMPLETST ; sample test suite (^STDASSERT protocol)
 new pass,fail
 do start^STDASSERT(.pass,.fail)
 do tAddsTwo(.pass,.fail)
 do tGreets(.pass,.fail)
 do report^STDASSERT(pass,fail)
 quit
 ;
tAddsTwo(pass,fail) ;@TEST "two plus two is four"
 do eq^STDASSERT(.pass,.fail,2+2,4,"adds")
 quit
 ;
tGreets(pass,fail) ; no @TEST tag here
 do eq^STDASSERT(.pass,.fail,"hi","hi","greet")
 quit
 ;
helper(x) ; not a test: wrong name shape, no pass/fail formals
 quit x

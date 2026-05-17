package lib

// Foo is the deliberately-named function the test analyzer flags. The
// analyzer emits one diagnostic for every FuncDecl named exactly "Foo".
func Foo() string { return "foo" }

// Bar should NOT be flagged.
func Bar() string { return "bar" }

package main

import (
	"fmt"

	"example.com/tiny/pkg/leaf"
)

func main() {
	s := &S{}
	s.A()
	fmt.Println(Foo())
	fmt.Println(leaf.Leaf())
}

// Foo is a top-level function used by find_symbol/references tests.
func Foo() string {
	return "foo"
}

type S struct{}

func (s *S) A() { s.B() }

func (s *S) B() { _ = leaf.Leaf() }

// I is satisfied by *S (used by implementations tests).
type I interface {
	A()
	B()
}

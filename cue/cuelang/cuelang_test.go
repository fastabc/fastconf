package cuelang

import "testing"

func TestCompileAndValidate(t *testing.T) {
	s, err := Compile(`{ port: int & >0 & <65536, name: string }`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if err := s.ValidateJSON([]byte(`{"port":8080,"name":"app"}`)); err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
	if err := s.ValidateJSON([]byte(`{"port":-1,"name":"app"}`)); err == nil {
		t.Fatalf("expected validation error for port=-1")
	}
	if err := s.ValidateJSON([]byte(`{"port":80}`)); err == nil {
		t.Fatalf("expected validation error for missing name")
	}
}

func TestCompileError(t *testing.T) {
	if _, err := Compile(`{ port: int & `); err == nil {
		t.Fatalf("expected compile error")
	}
}

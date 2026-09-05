package auth

import "testing"

func TestPassword(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := VerifyPassword(h, "correct horse battery staple"); !ok {
		t.Fatal("correct password rejected")
	}
	if ok, _ := VerifyPassword(h, "wrong"); ok {
		t.Fatal("wrong password accepted")
	}
	if Slugify("My Team  Space!") != "my-team-space" {
		t.Fatalf("slugify = %q", Slugify("My Team  Space!"))
	}
}

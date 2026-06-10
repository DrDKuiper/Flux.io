package auth

import "testing"

func TestHashAndCheckPassword(t *testing.T) {
	hash, err := HashPassword("s3cret-pass")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}
	if hash == "s3cret-pass" {
		t.Fatal("hash must not equal the plaintext")
	}
	if !CheckPassword(hash, "s3cret-pass") {
		t.Error("CheckPassword should accept the correct password")
	}
	if CheckPassword(hash, "wrong") {
		t.Error("CheckPassword should reject an incorrect password")
	}
}

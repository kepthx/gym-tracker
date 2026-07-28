package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	const password = "очень-секретный-пароль"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("захешировать: %v", err)
	}
	if strings.Contains(hash, password) {
		t.Fatal("пароль виден в хеше")
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("не формат PHC: %s", hash)
	}

	ok, err := VerifyPassword(password, hash)
	if err != nil || !ok {
		t.Fatalf("верный пароль не принят: ok=%v err=%v", ok, err)
	}

	ok, err = VerifyPassword("не-тот-пароль", hash)
	if err != nil {
		t.Fatalf("проверка неверного пароля дала ошибку: %v", err)
	}
	if ok {
		t.Fatal("принят неверный пароль")
	}
}

// The salt is random, so two hashes of the same password have to differ: otherwise the
// database shows who shares a password.
func TestHashesDifferForSamePassword(t *testing.T) {
	first, err := HashPassword("очень-секретный-пароль")
	if err != nil {
		t.Fatalf("первый хеш: %v", err)
	}
	second, err := HashPassword("очень-секретный-пароль")
	if err != nil {
		t.Fatalf("второй хеш: %v", err)
	}
	if first == second {
		t.Fatal("два хеша одного пароля совпали — соль не случайна")
	}
}

func TestVerifyRejectsGarbage(t *testing.T) {
	for _, bad := range []string{
		"",
		"просто строка",
		"$argon2i$v=19$m=65536,t=3,p=4$c29sdA$aGFzaA", // a different argon2 variant
		"$argon2id$v=19$m=65536,t=3,p=4$не-base64$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=4$c29sdA",
	} {
		if _, err := VerifyPassword("пароль", bad); err == nil {
			t.Errorf("мусорный хеш %q принят без ошибки", bad)
		}
	}
}

func TestTokenIsRandomAndHashed(t *testing.T) {
	first, firstHash, err := NewToken()
	if err != nil {
		t.Fatalf("выдать токен: %v", err)
	}
	second, _, err := NewToken()
	if err != nil {
		t.Fatalf("выдать второй токен: %v", err)
	}
	if first == second {
		t.Fatal("два токена подряд совпали")
	}
	if len(first) < 40 {
		t.Fatalf("токен слишком короткий: %d символов", len(first))
	}
	if len(firstHash) != 32 {
		t.Fatalf("хеш токена %d байт, ожидалось 32", len(firstHash))
	}
	if string(firstHash) == first {
		t.Fatal("в базу пошёл бы сам токен, а не его хеш")
	}
	if string(HashToken(first)) != string(firstHash) {
		t.Fatal("хеш токена не воспроизводится")
	}
}

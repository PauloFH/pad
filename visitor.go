package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	mrand "math/rand/v2"
	"strings"
)

// bichos e adjetivos formam o nome de visitante ("Capivara Anônima"): o adjetivo
// concorda em gênero com o bicho.
type bicho struct {
	nome string
	fem  bool
}

type adjetivo struct{ m, f string }

var bichos = []bicho{
	{"Capivara", true}, {"Onça", true}, {"Arara", true}, {"Preguiça", true}, {"Ariranha", true},
	{"Anta", true}, {"Jaguatirica", true}, {"Cutia", true}, {"Tartaruga", true}, {"Seriema", true},
	{"Coruja", true}, {"Suçuarana", true}, {"Lontra", true}, {"Piranha", true}, {"Garça", true},
	{"Jiboia", true}, {"Baleia", true}, {"Raposa", true}, {"Borboleta", true}, {"Cotia", true},
	{"Tatu", false}, {"Jabuti", false}, {"Gambá", false}, {"Tamanduá", false}, {"Boto", false},
	{"Jacaré", false}, {"Tucano", false}, {"Urubu", false}, {"Quati", false}, {"Sagui", false},
	{"Lobo-guará", false}, {"Beija-flor", false}, {"Pirarucu", false}, {"Curió", false}, {"Papagaio", false},
	{"Caranguejo", false}, {"Macaco-prego", false}, {"Bem-te-vi", false}, {"Sabiá", false}, {"Cavalo-marinho", false},
}

var adjetivos = []adjetivo{
	{"Anônimo", "Anônima"}, {"Curioso", "Curiosa"}, {"Apressado", "Apressada"}, {"Sonolento", "Sonolenta"},
	{"Sorridente", "Sorridente"}, {"Desconfiado", "Desconfiada"}, {"Faminto", "Faminta"}, {"Distraído", "Distraída"},
	{"Valente", "Valente"}, {"Tímido", "Tímida"}, {"Animado", "Animada"}, {"Elegante", "Elegante"},
	{"Misterioso", "Misteriosa"}, {"Teimoso", "Teimosa"}, {"Brincalhão", "Brincalhona"}, {"Preguiçoso", "Preguiçosa"},
	{"Faceiro", "Faceira"}, {"Esperto", "Esperta"}, {"Atrapalhado", "Atrapalhada"}, {"Corajoso", "Corajosa"},
	{"Sossegado", "Sossegada"}, {"Barulhento", "Barulhenta"}, {"Fofo", "Fofa"}, {"Gentil", "Gentil"},
	{"Pensativo", "Pensativa"}, {"Falante", "Falante"}, {"Folgado", "Folgada"}, {"Contente", "Contente"},
	{"Perdido", "Perdida"}, {"Sorrateiro", "Sorrateira"}, {"Veloz", "Veloz"}, {"Astuto", "Astuta"},
	{"Calmo", "Calma"}, {"Orgulhoso", "Orgulhosa"},
}

// loadVisitorKey devolve a chave de assinatura dos visitantes, criando-a na primeira vez.
// INSERT OR IGNORE + SELECT: dois processos (servidor e CLI) abrindo o banco juntos acabam com a mesma chave.
func loadVisitorKey(db *sql.DB) ([]byte, error) {
	fresh := make([]byte, 32)
	if _, err := rand.Read(fresh); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO secrets (k, v) VALUES ('visitor', ?)`, fresh); err != nil {
		return nil, err
	}
	var key []byte
	err := db.QueryRow(`SELECT v FROM secrets WHERE k = 'visitor'`).Scan(&key)
	return key, err
}

func (a *app) sign(payload string) []byte {
	m := hmac.New(sha256.New, a.visitorKey)
	m.Write([]byte(payload))
	return m.Sum(nil)
}

// newVisitor sorteia uma identidade; o token assinado deixa o navegador manter o nome sem poder inventar outro.
func (a *app) newVisitor() (id, name, token string) {
	raw := make([]byte, 9)
	rand.Read(raw) // nunca falha (Go 1.24+)
	id = base64.RawURLEncoding.EncodeToString(raw)
	b := bichos[mrand.IntN(len(bichos))]
	adj := adjetivos[mrand.IntN(len(adjetivos))]
	name = b.nome + " " + adj.m
	if b.fem {
		name = b.nome + " " + adj.f
	}
	payload := id + "|" + name
	enc := base64.RawURLEncoding
	return id, name, enc.EncodeToString([]byte(payload)) + "." + enc.EncodeToString(a.sign(payload))
}

// parseVisitor valida o token; qualquer defeito (formato, base64, assinatura) devolve ok=false.
func (a *app) parseVisitor(token string) (id, name string, ok bool) {
	p, s, found := strings.Cut(token, ".")
	if !found {
		return "", "", false
	}
	enc := base64.RawURLEncoding
	payload, err1 := enc.DecodeString(p)
	sig, err2 := enc.DecodeString(s)
	if err1 != nil || err2 != nil || !hmac.Equal(sig, a.sign(string(payload))) {
		return "", "", false
	}
	return strings.Cut(string(payload), "|")
}

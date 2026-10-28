package securebus_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/synamcps/synamcps-server/internal/access"
	"github.com/synamcps/synamcps-server/internal/agentkeys"
	"github.com/synamcps/synamcps-server/internal/models"
	"github.com/synamcps/synamcps-server/internal/policy"
	"github.com/synamcps/synamcps-server/internal/securebus"
)

func TestSecureBusPublishAttestVerify(t *testing.T) {
	ctx := context.Background()
	store := access.NewInMemoryStore()
	svc := access.NewService(store)
	owner := models.Principal{SubjectKey: "user:owner"}
	obj, err := store.CreateObject(ctx, models.DataObject{
		Kind: models.ObjectKindSecureBus, Name: "sb", OwnerSubjectKey: models.SubjectKeyForPrincipal(owner),
	}, models.SubjectKeyForPrincipal(owner))
	if err != nil {
		t.Fatal(err)
	}

	keyStore := agentkeys.NewStore(nil, nil, "test")
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := keyStore.CreateChallenge(ctx, "token:tok1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	envelope := map[string]any{
		"nonce": ch.Nonce, "publicKey": base64.RawURLEncoding.EncodeToString(pub), "tokenId": "tok1",
	}
	sig, err := securebus.SignEd25519(priv, envelope)
	if err != nil {
		t.Fatal(err)
	}
	ak, err := keyStore.Register(ctx, agentkeys.RegisterInput{
		SubjectKey: models.SubjectKeyForPrincipal(owner), TokenID: "tok1", Algorithm: "ed25519",
		PublicKey: pub, Nonce: ch.Nonce, Signature: sig, Binder: "token:tok1",
	})
	if err != nil {
		t.Fatal(err)
	}

	signer, err := securebus.NewPolicySignerFromMaterial("")
	if err != nil {
		t.Fatal(err)
	}
	busSvc := securebus.NewService(keyStore, signer, 300)
	opener := policy.NewOpener(svc, nil)
	ac := models.APIAccessContext{Principal: owner}

	scope, err := opener.Open(ctx, owner, ac, obj.ID, "publish")
	if err != nil {
		t.Fatal(err)
	}
	doc := securebus.Document{
		Schema: securebus.PolicySchemaID, TopicID: obj.ID, Version: 1,
		IssuedAt: time.Now().UTC().Format(time.RFC3339), Chain: false,
		Agents: map[string]securebus.AgentRef{
			"planner": {KeyID: ak.KeyID, Alg: "ed25519", PublicKey: ak.PublicKeyB64},
		},
		Rules: securebus.Rules{Publish: []string{"planner"}, Attest: []string{"planner"}, Subscribe: []string{"planner"}},
		Conditions: map[string]securebus.Condition{
			"approved": {MinOk: 1},
		},
	}
	pol, err := busSvc.PutPolicy(ctx, scope, doc, models.SubjectKeyForPrincipal(owner))
	if err != nil {
		t.Fatal(err)
	}

	scope2, err := opener.Open(ctx, owner, ac, obj.ID, "publish")
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Now().UTC().Truncate(time.Nanosecond)
	nonce := "n-1"
	msgEnv := map[string]any{
		"objectId": obj.ID, "policyVersion": float64(pol.Version), "policyHash": pol.Hash,
		"authorKeyId": ak.KeyID, "nonce": nonce, "createdAt": createdAt.Format(time.RFC3339Nano),
		"payload": map[string]any{"task": "deploy"},
	}
	msgSig, err := securebus.SignEd25519(priv, msgEnv)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := busSvc.Publish(ctx, scope2, securebus.PublishInput{
		KeyID: ak.KeyID, SubjectKey: models.SubjectKeyForPrincipal(owner), TokenID: "tok1",
		PolicyVersion: pol.Version, PolicyHash: pol.Hash, Nonce: nonce, CreatedAt: createdAt,
		Payload: map[string]any{"task": "deploy"}, Signature: securebus.B64(msgSig),
	})
	if err != nil {
		t.Fatal(err)
	}

	scope3, err := opener.Open(ctx, owner, ac, obj.ID, "attest")
	if err != nil {
		t.Fatal(err)
	}
	attAt := time.Now().UTC().Truncate(time.Nanosecond)
	attEnv := map[string]any{
		"messageId": float64(msg.ID), "messageHash": msg.Hash, "policyVersion": float64(pol.Version),
		"signerKeyId": ak.KeyID, "status": "ok", "comment": "lgtm", "createdAt": attAt.Format(time.RFC3339Nano),
	}
	attSig, err := securebus.SignEd25519(priv, attEnv)
	if err != nil {
		t.Fatal(err)
	}
	_, err = busSvc.Attest(ctx, scope3, securebus.AttestInput{
		KeyID: ak.KeyID, SubjectKey: models.SubjectKeyForPrincipal(owner), TokenID: "tok1",
		MessageID: msg.ID, PolicyVersion: pol.Version, Status: "ok", Comment: "lgtm",
		CreatedAt: attAt, Signature: securebus.B64(attSig),
	})
	if err != nil {
		t.Fatal(err)
	}

	scope4, err := opener.Open(ctx, owner, ac, obj.ID, "verify")
	if err != nil {
		t.Fatal(err)
	}
	vr, err := busSvc.Verify(ctx, scope4, msg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !vr.SignatureOK || !vr.HashOK {
		t.Fatalf("verify: %+v", vr)
	}

	scope5, err := opener.Open(ctx, owner, ac, obj.ID, "consume")
	if err != nil {
		t.Fatal(err)
	}
	res, err := busSvc.Consume(ctx, scope5, "c1", "approved", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 1 || res.Messages[0].ID != msg.ID {
		t.Fatalf("consume: %+v", res)
	}
}

func TestForeignObjectForbidden(t *testing.T) {
	ctx := context.Background()
	store := access.NewInMemoryStore()
	svc := access.NewService(store)
	a := models.Principal{SubjectKey: "user:a"}
	b := models.Principal{SubjectKey: "user:b"}
	obj, err := store.CreateObject(ctx, models.DataObject{
		Kind: models.ObjectKindSecureBus, Name: "sb", OwnerSubjectKey: models.SubjectKeyForPrincipal(a),
	}, models.SubjectKeyForPrincipal(a))
	if err != nil {
		t.Fatal(err)
	}
	opener := policy.NewOpener(svc, nil)
	_, err = opener.Open(ctx, b, models.APIAccessContext{Principal: b}, obj.ID, "policy")
	if err != policy.ErrForbidden {
		t.Fatalf("got %v", err)
	}
}

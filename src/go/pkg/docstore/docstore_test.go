package docstore

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"cloud.google.com/go/firestore/apiv1/firestorepb"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func open(t *testing.T) *Client {
	t.Helper()
	c, err := Open(filepath.Join(t.TempDir(), "fitglue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestGetMissingIsNotFound(t *testing.T) {
	c := open(t)
	snap, err := c.Collection("users").Doc("nobody").Get(context.Background())
	if status.Code(err) != codes.NotFound {
		t.Fatalf("want NotFound, got %v", err)
	}
	if snap == nil || snap.Exists() {
		t.Fatal("want a non-existent snapshot")
	}
}

func TestRoundTripTypes(t *testing.T) {
	c := open(t)
	ctx := context.Background()
	when := time.Date(2026, 11, 18, 10, 0, 0, 123456000, time.UTC)
	in := map[string]interface{}{
		"i":    int32(7),
		"f":    3.0,
		"f2":   2.5,
		"s":    "x",
		"b":    true,
		"t":    when,
		"raw":  []byte{1, 2, 3},
		"nil":  nil,
		"list": []string{"a", "b"},
		"nest": map[string]interface{}{"deep": map[string]interface{}{"n": 1}},
		"tags": map[string]string{"k": "v"},
	}
	d := c.Collection("users").Doc("u1")
	if _, err := d.Set(ctx, in); err != nil {
		t.Fatal(err)
	}
	snap, err := d.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := snap.Data()
	if v, ok := m["i"].(int64); !ok || v != 7 {
		t.Errorf("i = %#v", m["i"])
	}
	if v, ok := m["f"].(float64); !ok || v != 3.0 {
		t.Errorf("f = %#v", m["f"])
	}
	if v, ok := m["f2"].(float64); !ok || v != 2.5 {
		t.Errorf("f2 = %#v", m["f2"])
	}
	if v, ok := m["t"].(time.Time); !ok || !v.Equal(when) {
		t.Errorf("t = %#v", m["t"])
	}
	if v, ok := m["raw"].([]byte); !ok || len(v) != 3 {
		t.Errorf("raw = %#v", m["raw"])
	}
	if _, ok := m["nil"]; !ok || m["nil"] != nil {
		t.Errorf("nil = %#v", m["nil"])
	}
	if l, ok := m["list"].([]interface{}); !ok || len(l) != 2 || l[1] != "b" {
		t.Errorf("list = %#v", m["list"])
	}
	if v, err := snap.DataAt("nest.deep.n"); err != nil || v != int64(1) {
		t.Errorf("DataAt = %#v, %v", v, err)
	}
	if v, _ := snap.DataAt("tags.k"); v != "v" {
		t.Errorf("tags.k = %#v", v)
	}
	if _, err := snap.DataAt("nope"); err == nil {
		t.Error("DataAt of missing field should fail")
	}
}

func TestSetReplacesAndMergeAllMerges(t *testing.T) {
	c := open(t)
	ctx := context.Background()
	d := c.Collection("users").Doc("u1").Collection("integrations").Doc("hevy")
	if _, err := d.Set(ctx, map[string]interface{}{"a": 1, "m": map[string]interface{}{"x": 1, "y": 2}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Set(ctx, map[string]interface{}{"b": 2, "m": map[string]interface{}{"y": 3}, "a": Delete}, MergeAll); err != nil {
		t.Fatal(err)
	}
	snap, _ := d.Get(ctx)
	m := snap.Data()
	if _, ok := m["a"]; ok {
		t.Error("a should be deleted")
	}
	if m["b"] != int64(2) {
		t.Errorf("b = %#v", m["b"])
	}
	nm := m["m"].(map[string]interface{})
	if nm["x"] != int64(1) || nm["y"] != int64(3) {
		t.Errorf("m = %#v", nm)
	}
	if _, err := d.Set(ctx, map[string]interface{}{"only": true}); err != nil {
		t.Fatal(err)
	}
	snap, _ = d.Get(ctx)
	if len(snap.Data()) != 1 {
		t.Errorf("plain Set should replace, got %#v", snap.Data())
	}
}

func TestUpdate(t *testing.T) {
	c := open(t)
	ctx := context.Background()
	d := c.Collection("users").Doc("u1")
	if _, err := d.Update(ctx, []Update{{Path: "x", Value: 1}}); status.Code(err) != codes.NotFound {
		t.Fatalf("Update of missing doc: want NotFound, got %v", err)
	}
	_, _ = d.Set(ctx, map[string]interface{}{"integrations": map[string]interface{}{"hevy": map[string]interface{}{"enabled": true}, "strava": map[string]interface{}{}}})
	if _, err := d.Update(ctx, []Update{
		{Path: "integrations.hevy.api_key", Value: "k"},
		{Path: "integrations.strava", Value: Delete},
	}); err != nil {
		t.Fatal(err)
	}
	snap, _ := d.Get(ctx)
	if v, _ := snap.DataAt("integrations.hevy.api_key"); v != "k" {
		t.Errorf("api_key = %#v", v)
	}
	if v, _ := snap.DataAt("integrations.hevy.enabled"); v != true {
		t.Errorf("enabled lost: %#v", v)
	}
	if _, err := snap.DataAt("integrations.strava"); err == nil {
		t.Error("strava should be deleted")
	}
}

func TestQuery(t *testing.T) {
	c := open(t)
	ctx := context.Background()
	runs := c.Collection("users").Doc("u1").Collection("pipeline_runs")
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for i, p := range []string{"a", "b", "a", "a"} {
		_, _ = runs.Doc(string(rune('w'+i))).Set(ctx, map[string]interface{}{
			"pipeline_id": p, "created_at": base.Add(time.Duration(i) * time.Hour), "n": i,
		})
	}
	_, _ = runs.Doc("nodate").Set(ctx, map[string]interface{}{"pipeline_id": "a"})
	// A sub-collection's documents are not part of the parent collection.
	_, _ = runs.Doc("w").Collection("x").Doc("y").Set(ctx, map[string]interface{}{"pipeline_id": "a"})

	docs, err := runs.Where("pipeline_id", "==", "a").OrderBy("created_at", Desc).Limit(2).Documents(ctx).GetAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 || docs[0].Ref.ID != "z" || docs[1].Ref.ID != "y" {
		t.Fatalf("got %v", ids(docs))
	}

	docs, _ = runs.Where("created_at", ">=", base.Add(time.Hour)).Where("created_at", "<=", base.Add(2*time.Hour)).Documents(ctx).GetAll()
	if len(docs) != 2 {
		t.Fatalf("range got %v", ids(docs))
	}

	docs, _ = runs.Where("n", ">", 1.5).Documents(ctx).GetAll()
	if len(docs) != 2 {
		t.Fatalf("int vs float compare got %v", ids(docs))
	}

	docs, _ = runs.Documents(ctx).GetAll()
	if len(docs) != 5 {
		t.Fatalf("collection has %d docs", len(docs))
	}

	it := runs.OrderBy("created_at", Desc).Offset(3).Documents(ctx)
	defer it.Stop()
	if d, err := it.Next(); err != nil || d.Ref.ID != "w" {
		t.Fatalf("offset: %v %v", d, err)
	}
	if _, err := it.Next(); !errors.Is(err, iterator.Done) {
		t.Fatalf("want Done, got %v", err)
	}

	res, err := runs.Where("pipeline_id", "==", "a").NewAggregationQuery().WithCount("total").Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v := res["total"].(*firestorepb.Value).GetIntegerValue(); v != 4 {
		t.Fatalf("count = %d", v)
	}
}

func TestNestedWhere(t *testing.T) {
	c := open(t)
	ctx := context.Background()
	_, _ = c.Collection("users").Doc("u1").Set(ctx, map[string]interface{}{"integrations": map[string]interface{}{"hevy": map[string]interface{}{"ingress_key": "abc"}}})
	_, _ = c.Collection("users").Doc("u2").Set(ctx, map[string]interface{}{})
	docs, _ := c.Collection("users").Where("integrations.hevy.ingress_key", "==", "abc").Limit(1).Documents(ctx).GetAll()
	if len(docs) != 1 || docs[0].Ref.ID != "u1" {
		t.Fatalf("got %v", ids(docs))
	}
}

func TestTransactionAndCreate(t *testing.T) {
	c := open(t)
	ctx := context.Background()
	claim := c.Collection("claims").Doc("e1")
	claimOnce := func() error {
		return c.RunTransaction(ctx, func(ctx context.Context, tx *Transaction) error {
			snap, err := tx.Get(claim)
			if err != nil && status.Code(err) != codes.NotFound {
				return err
			}
			if snap.Exists() {
				return errors.New("already claimed")
			}
			return tx.Set(claim, map[string]interface{}{"claimed_at": time.Now()})
		})
	}
	if err := claimOnce(); err != nil {
		t.Fatal(err)
	}
	if err := claimOnce(); err == nil {
		t.Fatal("second claim should fail")
	}
	// A failed transaction leaves nothing behind.
	_ = c.RunTransaction(ctx, func(ctx context.Context, tx *Transaction) error {
		_ = tx.Set(c.Collection("claims").Doc("e2"), map[string]interface{}{"x": 1})
		return errors.New("abort")
	})
	if _, err := c.Collection("claims").Doc("e2").Get(ctx); status.Code(err) != codes.NotFound {
		t.Fatal("aborted write was committed")
	}
	if _, err := claim.Create(ctx, map[string]interface{}{}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("Create of existing doc: %v", err)
	}
	ref, _, err := c.Collection("claims").Add(ctx, map[string]interface{}{"x": 1})
	if err != nil || len(ref.ID) != 20 {
		t.Fatalf("Add: %v %v", ref, err)
	}
	if _, err := ref.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := ref.Get(ctx); status.Code(err) != codes.NotFound {
		t.Fatal("delete did not delete")
	}
}

func TestDocPaths(t *testing.T) {
	c := open(t)
	d := c.Doc("users/u1/integrations/hevy")
	if d.ID != "hevy" || d.Parent.Path != "users/u1/integrations" || d.Parent.Parent.ID != "u1" {
		t.Fatalf("%+v", d)
	}
	if c.Doc("users") != nil || c.Collection("users/u1") != nil {
		t.Fatal("wrong segment counts should give nil")
	}
}

func ids(docs []*DocumentSnapshot) []string {
	var out []string
	for _, d := range docs {
		out = append(out, d.Ref.ID)
	}
	return out
}

func TestImportIsRerunnable(t *testing.T) {
	c := open(t)
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	if r, err := c.Import(ctx, "users/u1", map[string]interface{}{"a": 1, "b": 1}, t0, t0); err != nil || r != Imported {
		t.Fatalf("first import: %v %v", r, err)
	}
	if r, _ := c.Import(ctx, "users/u1", map[string]interface{}{"a": 1, "b": 1}, t0, t0); r != Unchanged {
		t.Fatalf("same version: %v", r)
	}
	// Source changed later: it replaces the local copy.
	if r, _ := c.Import(ctx, "users/u1", map[string]interface{}{"a": 2}, t0, t1); r != Imported {
		t.Fatalf("newer source: %v", r)
	}
	snap, _ := c.Doc("users/u1").Get(ctx)
	if _, ok := snap.Data()["b"]; ok || snap.Data()["a"] != int64(2) {
		t.Fatalf("got %v", snap.Data())
	}
	// Changed locally after the source: local fields win, source fields fill in.
	c.now = func() time.Time { return t1.Add(time.Hour) }
	_, _ = c.Doc("users/u1").Set(ctx, map[string]interface{}{"a": 3, "local": true})
	if r, _ := c.Import(ctx, "users/u1", map[string]interface{}{"a": 2, "remote": true}, t0, t1); r != Merged {
		t.Fatalf("local newer: %v", r)
	}
	snap, _ = c.Doc("users/u1").Get(ctx)
	m := snap.Data()
	if m["a"] != int64(3) || m["local"] != true || m["remote"] != true {
		t.Fatalf("merge got %v", m)
	}
	counts, err := c.CountByCollection(ctx)
	if err != nil || counts["users"] != 1 {
		t.Fatalf("counts %v %v", counts, err)
	}
	if CollectionKind("users/u1/pipeline_runs") != "users/*/pipeline_runs" {
		t.Fatal(CollectionKind("users/u1/pipeline_runs"))
	}
}

func TestStructsAndProtoTimestamps(t *testing.T) {
	c := open(t)
	ctx := context.Background()
	type profile struct {
		DisplayName string
		Email       string `firestore:"email,omitempty"`
		Secret      string `firestore:"-"`
		hidden      int
		When        *timestamppb.Timestamp
	}
	when := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	d := c.Doc("users/u1")
	if _, err := d.Set(ctx, &profile{DisplayName: "J", Secret: "s", hidden: 1, When: timestamppb.New(when)}); err != nil {
		t.Fatal(err)
	}
	snap, _ := d.Get(ctx)
	m := snap.Data()
	if m["DisplayName"] != "J" || len(m) != 2 {
		t.Fatalf("got %v", m)
	}
	if v, ok := m["When"].(time.Time); !ok || !v.Equal(when) {
		t.Fatalf("When = %#v", m["When"])
	}
}

func TestSnapshot(t *testing.T) {
	c := open(t)
	ctx := context.Background()
	_, _ = c.Doc("users/u1").Set(ctx, map[string]interface{}{"a": 1})
	out := filepath.Join(t.TempDir(), "snap", "fitglue.db")
	for i := 0; i < 2; i++ { // a second run replaces the first
		if n, err := c.Snapshot(ctx, out); err != nil || n == 0 {
			t.Fatalf("snapshot: %d %v", n, err)
		}
	}
	s, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if snap, err := s.Doc("users/u1").Get(ctx); err != nil || snap.Data()["a"] != int64(1) {
		t.Fatalf("snapshot content: %v", err)
	}
}

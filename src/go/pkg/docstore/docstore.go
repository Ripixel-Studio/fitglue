// Package docstore is FitGlue's document store on the box: Firestore's data
// model (collections, documents, sub-collections, field maps) kept in one
// SQLite table inside the same file as the message queue (FITGLUE_DB).
//
// Its API mirrors the slice of cloud.google.com/go/firestore that FitGlue
// uses, with the same names and signatures, so a store moves over by changing
// its import to
//
//	firestore "github.com/fitglue/server/src/go/pkg/docstore"
//
// Behaviour follows Firestore where FitGlue relies on it: Get of a missing
// document returns a NotFound gRPC status (and a snapshot with Exists false),
// Set replaces unless MergeAll is passed, Update fails on a missing document
// and takes dotted paths, Delete as a value removes a field, a query skips
// documents that lack a field it filters or orders on, and Data() gives back
// int64 / float64 / time.Time / []byte as Firestore does.
//
// Queries filter and sort in Go after reading the collection's rows: FitGlue
// has one user and collections of a few thousand documents at most.
package docstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/firestore/apiv1/firestorepb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/fitglue/server/src/go/pkg/infrastructure/localqueue"
)

const schema = `
CREATE TABLE IF NOT EXISTS docs (
	path        TEXT PRIMARY KEY,
	parent      TEXT NOT NULL,
	id          TEXT NOT NULL,
	data        TEXT NOT NULL,
	create_time INTEGER NOT NULL,
	update_time INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS docs_parent ON docs(parent);
`

type sentinel int

// Delete, used as a field value in Set(MergeAll) or Update, removes the field.
const Delete sentinel = 1

// SetOption is accepted by Set; MergeAll is the only one FitGlue uses.
type SetOption interface{ isSetOption() }

type mergeAllOption struct{}

func (mergeAllOption) isSetOption() {}

// MergeAll merges the given map into the stored document instead of
// replacing it.
var MergeAll SetOption = mergeAllOption{}

// Direction orders query results.
type Direction int

const (
	Asc  Direction = 1
	Desc Direction = 2
)

// Update is one field change for DocumentRef.Update.
type Update struct {
	Path      string
	FieldPath []string
	Value     interface{}
}

// WriteResult mirrors firestore.WriteResult.
type WriteResult struct {
	UpdateTime time.Time
}

// TransactionOption is accepted and ignored (SQLite transactions are serial).
type TransactionOption interface{}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Client is a handle on the docs table.
type Client struct {
	db  *sql.DB
	now func() time.Time
}

// NewClient opens the box's SQLite file (FITGLUE_DB, default
// ~/.local/share/fitglue/fitglue.db). projectID and opts are accepted so call
// sites keep Firestore's signature; they are ignored.
func NewClient(ctx context.Context, projectID string, opts ...option.ClientOption) (*Client, error) {
	return Open(localqueue.DefaultPath())
}

// Open opens (creating if needed) the SQLite file at path.
func Open(path string) (*Client, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("docstore: create %s: %w", dir, err)
		}
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("docstore: open %s: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("docstore: schema: %w", err)
	}
	return &Client{db: db, now: time.Now}, nil
}

// Close closes the database.
func (c *Client) Close() error { return c.db.Close() }

// DB exposes the handle for maintenance commands (counts, snapshots).
func (c *Client) DB() *sql.DB { return c.db }

// Collection returns a top-level collection, or a sub-collection when path
// has an odd number of segments ("users/u1/integrations").
func (c *Client) Collection(path string) *CollectionRef {
	parts := strings.Split(path, "/")
	if len(parts)%2 == 0 {
		return nil
	}
	var parent *DocumentRef
	if len(parts) > 1 {
		parent = c.Doc(strings.Join(parts[:len(parts)-1], "/"))
	}
	return &CollectionRef{c: c, ID: parts[len(parts)-1], Path: path, Parent: parent, query: Query{c: c, parent: path}}
}

// Doc returns a document by its full path ("users/u1").
func (c *Client) Doc(path string) *DocumentRef {
	parts := strings.Split(path, "/")
	if len(parts)%2 != 0 {
		return nil
	}
	return c.Collection(strings.Join(parts[:len(parts)-1], "/")).Doc(parts[len(parts)-1])
}

// CollectionRef is a collection: a Query over its direct documents.
type CollectionRef struct {
	c      *Client
	ID     string
	Path   string
	Parent *DocumentRef
	query  Query
}

// Doc returns the document with the given id in this collection.
func (cr *CollectionRef) Doc(id string) *DocumentRef {
	return &DocumentRef{c: cr.c, ID: id, Path: cr.Path + "/" + id, Parent: cr}
}

// NewDoc returns a document with a fresh random id.
func (cr *CollectionRef) NewDoc() *DocumentRef { return cr.Doc(newID()) }

// Add creates a document with a fresh id.
func (cr *CollectionRef) Add(ctx context.Context, data interface{}) (*DocumentRef, *WriteResult, error) {
	d := cr.NewDoc()
	wr, err := d.Create(ctx, data)
	if err != nil {
		return nil, nil, err
	}
	return d, wr, nil
}

// Query helpers on a collection.
func (cr *CollectionRef) Where(path, op string, value interface{}) Query {
	return cr.query.Where(path, op, value)
}
func (cr *CollectionRef) OrderBy(path string, dir Direction) Query {
	return cr.query.OrderBy(path, dir)
}
func (cr *CollectionRef) Limit(n int) Query  { return cr.query.Limit(n) }
func (cr *CollectionRef) Offset(n int) Query { return cr.query.Offset(n) }
func (cr *CollectionRef) Documents(ctx context.Context) *DocumentIterator {
	return cr.query.Documents(ctx)
}
func (cr *CollectionRef) NewAggregationQuery() *AggregationQuery {
	return cr.query.NewAggregationQuery()
}

// DocumentRef is one document.
type DocumentRef struct {
	c      *Client
	ID     string
	Path   string
	Parent *CollectionRef
}

// Collection returns a sub-collection of this document.
func (d *DocumentRef) Collection(id string) *CollectionRef {
	path := d.Path + "/" + id
	return &CollectionRef{c: d.c, ID: id, Path: path, Parent: d, query: Query{c: d.c, parent: path}}
}

func (d *DocumentRef) Get(ctx context.Context) (*DocumentSnapshot, error) {
	return getDoc(ctx, d.c.db, d)
}

func (d *DocumentRef) Set(ctx context.Context, data interface{}, opts ...SetOption) (*WriteResult, error) {
	return setDoc(ctx, d.c, d.c.db, d, data, opts...)
}

func (d *DocumentRef) Create(ctx context.Context, data interface{}) (*WriteResult, error) {
	var wr *WriteResult
	err := d.c.RunTransaction(ctx, func(ctx context.Context, tx *Transaction) error {
		return tx.Create(d, data)
	})
	if err != nil {
		return nil, err
	}
	wr = &WriteResult{UpdateTime: d.c.now()}
	return wr, nil
}

func (d *DocumentRef) Update(ctx context.Context, updates []Update) (*WriteResult, error) {
	var wr *WriteResult
	err := d.c.RunTransaction(ctx, func(ctx context.Context, tx *Transaction) error {
		return tx.Update(d, updates)
	})
	if err != nil {
		return nil, err
	}
	wr = &WriteResult{UpdateTime: d.c.now()}
	return wr, nil
}

func (d *DocumentRef) Delete(ctx context.Context) (*WriteResult, error) {
	if _, err := d.c.db.ExecContext(ctx, `DELETE FROM docs WHERE path = ?`, d.Path); err != nil {
		return nil, err
	}
	return &WriteResult{UpdateTime: d.c.now()}, nil
}

// DocumentSnapshot is a read document. A snapshot of a missing document has
// Exists() false and empty Data().
type DocumentSnapshot struct {
	Ref        *DocumentRef
	CreateTime time.Time
	UpdateTime time.Time
	ReadTime   time.Time
	raw        string
	m          map[string]interface{}
}

func (s *DocumentSnapshot) Exists() bool { return s != nil && s.m != nil }

// Data returns a fresh copy of the document's fields.
func (s *DocumentSnapshot) Data() map[string]interface{} {
	if !s.Exists() {
		return nil
	}
	m, err := decodeDoc(s.raw)
	if err != nil {
		return nil
	}
	return m
}

// DataAt returns the value at a dotted field path.
func (s *DocumentSnapshot) DataAt(path string) (interface{}, error) {
	if !s.Exists() {
		return nil, status.Errorf(codes.NotFound, "document %s does not exist", s.Ref.Path)
	}
	v, ok := getPath(s.Data(), path)
	if !ok {
		return nil, fmt.Errorf("docstore: no field %q", path)
	}
	return v, nil
}

func getDoc(ctx context.Context, ex execer, d *DocumentRef) (*DocumentSnapshot, error) {
	var raw string
	var ct, ut int64
	err := ex.QueryRowContext(ctx, `SELECT data, create_time, update_time FROM docs WHERE path = ?`, d.Path).Scan(&raw, &ct, &ut)
	if errors.Is(err, sql.ErrNoRows) {
		return &DocumentSnapshot{Ref: d, ReadTime: time.Now()}, status.Errorf(codes.NotFound, "%q not found", d.Path)
	}
	if err != nil {
		return nil, err
	}
	m, err := decodeDoc(raw)
	if err != nil {
		return nil, fmt.Errorf("docstore: decode %s: %w", d.Path, err)
	}
	return &DocumentSnapshot{Ref: d, raw: raw, m: m, CreateTime: time.UnixMicro(ct), UpdateTime: time.UnixMicro(ut), ReadTime: time.Now()}, nil
}

func toMap(data interface{}) (map[string]interface{}, error) {
	n, err := normalize(data)
	if err != nil {
		return nil, err
	}
	m, ok := n.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("docstore: document data must be a map, got %T", data)
	}
	return m, nil
}

func setDoc(ctx context.Context, c *Client, ex execer, d *DocumentRef, data interface{}, opts ...SetOption) (*WriteResult, error) {
	m, err := toMap(data)
	if err != nil {
		return nil, err
	}
	merge := false
	for _, o := range opts {
		if o == MergeAll {
			merge = true
		}
	}
	if merge {
		// Read-modify-write needs a transaction unless we're already in one.
		if db, ok := ex.(*sql.DB); ok {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return nil, err
			}
			defer tx.Rollback() //nolint:errcheck
			if err := mergeInto(ctx, c, tx, d, m); err != nil {
				return nil, err
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
		} else if err := mergeInto(ctx, c, ex, d, m); err != nil {
			return nil, err
		}
		return &WriteResult{UpdateTime: c.now()}, nil
	}
	stripDeletes(m)
	if err := writeDoc(ctx, c, ex, d, m); err != nil {
		return nil, err
	}
	return &WriteResult{UpdateTime: c.now()}, nil
}

func mergeInto(ctx context.Context, c *Client, ex execer, d *DocumentRef, m map[string]interface{}) error {
	snap, err := getDoc(ctx, ex, d)
	if err != nil && status.Code(err) != codes.NotFound {
		return err
	}
	cur := map[string]interface{}{}
	if snap.Exists() {
		cur = snap.m
	}
	mergeAll(cur, m)
	return writeDoc(ctx, c, ex, d, cur)
}

func writeDoc(ctx context.Context, c *Client, ex execer, d *DocumentRef, m map[string]interface{}) error {
	raw, err := encodeDoc(m)
	if err != nil {
		return err
	}
	now := c.now().UnixMicro()
	_, err = ex.ExecContext(ctx, `INSERT INTO docs (path, parent, id, data, create_time, update_time) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET data = excluded.data, update_time = excluded.update_time`,
		d.Path, d.Parent.Path, d.ID, raw, now, now)
	return err
}

// Transaction runs reads and writes atomically (an IMMEDIATE SQLite
// transaction, so concurrent writers wait rather than conflict).
type Transaction struct {
	c  *Client
	tx *sql.Tx
}

// RunTransaction runs f in one transaction, committing when it returns nil.
func (c *Client) RunTransaction(ctx context.Context, f func(context.Context, *Transaction) error, opts ...TransactionOption) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := f(ctx, &Transaction{c: c, tx: tx}); err != nil {
		return err
	}
	return tx.Commit()
}

func (t *Transaction) Get(d *DocumentRef) (*DocumentSnapshot, error) {
	return getDoc(context.Background(), t.tx, d)
}

func (t *Transaction) Set(d *DocumentRef, data interface{}, opts ...SetOption) error {
	_, err := setDoc(context.Background(), t.c, t.tx, d, data, opts...)
	return err
}

func (t *Transaction) Create(d *DocumentRef, data interface{}) error {
	snap, err := getDoc(context.Background(), t.tx, d)
	if err != nil && status.Code(err) != codes.NotFound {
		return err
	}
	if snap.Exists() {
		return status.Errorf(codes.AlreadyExists, "%q already exists", d.Path)
	}
	_, err = setDoc(context.Background(), t.c, t.tx, d, data)
	return err
}

func (t *Transaction) Update(d *DocumentRef, updates []Update) error {
	ctx := context.Background()
	snap, err := getDoc(ctx, t.tx, d)
	if err != nil {
		return err
	}
	cur := snap.m
	for _, u := range updates {
		path := u.Path
		if path == "" {
			path = strings.Join(u.FieldPath, ".")
		}
		var v interface{} = Delete
		if u.Value != Delete {
			if v, err = normalize(u.Value); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			if vm, ok := v.(map[string]interface{}); ok {
				stripDeletes(vm)
			}
		}
		setPath(cur, path, v)
	}
	return writeDoc(ctx, t.c, t.tx, d, cur)
}

func (t *Transaction) Delete(d *DocumentRef) error {
	_, err := t.tx.Exec(`DELETE FROM docs WHERE path = ?`, d.Path)
	return err
}

func (t *Transaction) Documents(q Query) *DocumentIterator {
	return q.run(context.Background(), t.tx)
}

type filter struct {
	path  string
	op    string
	value interface{}
}

type order struct {
	path string
	dir  Direction
}

// Query is an immutable query over one collection's direct documents.
type Query struct {
	c       *Client
	parent  string
	filters []filter
	orders  []order
	group   string // collection-group query: every collection with this id
	limit   int
	offset  int
	after   *DocumentSnapshot
	err     error
}

// CollectionGroup queries every collection named id, at any depth.
func (c *Client) CollectionGroup(id string) Query {
	return Query{c: c, group: id}
}

// Select is accepted for compatibility; documents always come back whole.
func (q Query) Select(paths ...string) Query { return q }

// StartAfter starts the results after the given document in this query's order.
func (q Query) StartAfter(snap *DocumentSnapshot) Query {
	q = q.clone()
	q.after = snap
	return q
}

func (q Query) clone() Query {
	q.filters = append([]filter(nil), q.filters...)
	q.orders = append([]order(nil), q.orders...)
	return q
}

func (q Query) Where(path, op string, value interface{}) Query {
	q = q.clone()
	v, err := normalize(value)
	if err != nil && q.err == nil {
		q.err = err
	}
	q.filters = append(q.filters, filter{path: path, op: op, value: v})
	return q
}

func (q Query) OrderBy(path string, dir Direction) Query {
	q = q.clone()
	q.orders = append(q.orders, order{path: path, dir: dir})
	return q
}

func (q Query) Limit(n int) Query  { q = q.clone(); q.limit = n; return q }
func (q Query) Offset(n int) Query { q = q.clone(); q.offset = n; return q }

func (q Query) Documents(ctx context.Context) *DocumentIterator {
	return q.run(ctx, q.c.db)
}

func (q Query) run(ctx context.Context, ex execer) *DocumentIterator {
	if q.err != nil {
		return &DocumentIterator{err: q.err}
	}
	docs, err := q.fetch(ctx, ex)
	return &DocumentIterator{docs: docs, err: err}
}

func (q Query) fetch(ctx context.Context, ex execer) ([]*DocumentSnapshot, error) {
	var rows *sql.Rows
	var err error
	if q.group != "" {
		rows, err = ex.QueryContext(ctx, `SELECT parent, id, data, create_time, update_time FROM docs WHERE parent = ? OR parent LIKE ?`,
			q.group, "%/"+q.group)
	} else {
		rows, err = ex.QueryContext(ctx, `SELECT parent, id, data, create_time, update_time FROM docs WHERE parent = ?`, q.parent)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	colls := map[string]*CollectionRef{}
	var out []*DocumentSnapshot
	now := time.Now()
	for rows.Next() {
		var parent, id, raw string
		var ct, ut int64
		if err := rows.Scan(&parent, &id, &raw, &ct, &ut); err != nil {
			return nil, err
		}
		coll := colls[parent]
		if coll == nil {
			coll = q.c.Collection(parent)
			colls[parent] = coll
		}
		m, err := decodeDoc(raw)
		if err != nil {
			return nil, fmt.Errorf("docstore: decode %s/%s: %w", q.parent, id, err)
		}
		if !q.matches(m) {
			continue
		}
		out = append(out, &DocumentSnapshot{Ref: coll.Doc(id), raw: raw, m: m,
			CreateTime: time.UnixMicro(ct), UpdateTime: time.UnixMicro(ut), ReadTime: now})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return q.cmp(out[i], out[j]) < 0 })
	if q.after != nil && q.after.Exists() {
		k := sort.Search(len(out), func(i int) bool { return q.cmp(out[i], q.after) > 0 })
		out = out[k:]
	}
	if q.offset > 0 {
		if q.offset >= len(out) {
			out = nil
		} else {
			out = out[q.offset:]
		}
	}
	if q.limit > 0 && len(out) > q.limit {
		out = out[:q.limit]
	}
	return out, nil
}

// cmp orders two documents by the query's OrderBy fields, then by path.
func (q Query) cmp(x, y *DocumentSnapshot) int {
	for _, o := range q.orders {
		a, _ := getPath(x.m, o.path)
		b, _ := getPath(y.m, o.path)
		c := compare(a, b)
		if o.dir == Desc {
			c = -c
		}
		if c != 0 {
			return c
		}
	}
	return strings.Compare(x.Ref.Path, y.Ref.Path)
}

func (q Query) matches(m map[string]interface{}) bool {
	for _, o := range q.orders {
		if _, ok := getPath(m, o.path); !ok {
			return false
		}
	}
	for _, f := range q.filters {
		v, ok := getPath(m, f.path)
		if !ok {
			return false
		}
		if !matchOp(v, f.op, f.value) {
			return false
		}
	}
	return true
}

func sameKind(a, b interface{}) bool { return typeRank(a) == typeRank(b) }

func matchOp(v interface{}, op string, want interface{}) bool {
	switch op {
	case "==":
		return sameKind(v, want) && compare(v, want) == 0
	case "!=":
		return v != nil && !(sameKind(v, want) && compare(v, want) == 0)
	case "<":
		return sameKind(v, want) && compare(v, want) < 0
	case "<=":
		return sameKind(v, want) && compare(v, want) <= 0
	case ">":
		return sameKind(v, want) && compare(v, want) > 0
	case ">=":
		return sameKind(v, want) && compare(v, want) >= 0
	case "in", "not-in":
		list, _ := want.([]interface{})
		found := false
		for _, w := range list {
			if sameKind(v, w) && compare(v, w) == 0 {
				found = true
				break
			}
		}
		if op == "in" {
			return found
		}
		return !found && v != nil
	case "array-contains":
		arr, _ := v.([]interface{})
		for _, e := range arr {
			if sameKind(e, want) && compare(e, want) == 0 {
				return true
			}
		}
		return false
	case "array-contains-any":
		arr, _ := v.([]interface{})
		list, _ := want.([]interface{})
		for _, e := range arr {
			for _, w := range list {
				if sameKind(e, w) && compare(e, w) == 0 {
					return true
				}
			}
		}
		return false
	}
	return false
}

// DocumentIterator walks query results.
type DocumentIterator struct {
	docs []*DocumentSnapshot
	i    int
	err  error
}

// Next returns the next document, or iterator.Done.
func (it *DocumentIterator) Next() (*DocumentSnapshot, error) {
	if it.err != nil {
		return nil, it.err
	}
	if it.i >= len(it.docs) {
		return nil, iterator.Done
	}
	d := it.docs[it.i]
	it.i++
	return d, nil
}

// GetAll returns every remaining document.
func (it *DocumentIterator) GetAll() ([]*DocumentSnapshot, error) {
	if it.err != nil {
		return nil, it.err
	}
	rest := it.docs[it.i:]
	it.i = len(it.docs)
	return rest, nil
}

// Stop releases the iterator (results are already in memory).
func (it *DocumentIterator) Stop() {}

// AggregationQuery counts a query's results.
type AggregationQuery struct {
	q      Query
	counts []string
}

// AggregationResult maps each alias to a *firestorepb.Value, as Firestore's does.
type AggregationResult map[string]interface{}

func (q Query) NewAggregationQuery() *AggregationQuery { return &AggregationQuery{q: q} }

func (a *AggregationQuery) WithCount(alias string) *AggregationQuery {
	a.counts = append(a.counts, alias)
	return a
}

func (a *AggregationQuery) Get(ctx context.Context) (AggregationResult, error) {
	docs, err := a.q.Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	res := AggregationResult{}
	for _, alias := range a.counts {
		res[alias] = &firestorepb.Value{ValueType: &firestorepb.Value_IntegerValue{IntegerValue: int64(len(docs))}}
	}
	return res, nil
}

const idChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func newID() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = idChars[int(b[i])%len(idChars)]
	}
	return string(b)
}

// ImportResult says what Import did with one document.
type ImportResult int

const (
	Imported  ImportResult = iota // written from the source
	Merged                        // local copy was newer: source fields under local ones
	Unchanged                     // local copy already had this version
)

// Import writes a document copied from another store (the Firestore
// migration), keeping its create and update times. It is safe to re-run: a
// document whose local copy has the same update time is left alone, and one
// changed locally since (update time newer than the source's) keeps its local
// fields over the source's.
func (c *Client) Import(ctx context.Context, path string, data map[string]interface{}, created, updated time.Time) (ImportResult, error) {
	d := c.Doc(path)
	if d == nil {
		return 0, fmt.Errorf("docstore: %q is not a document path", path)
	}
	m, err := toMap(data)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	stripDeletes(m)
	result := Imported
	err = c.RunTransaction(ctx, func(ctx context.Context, tx *Transaction) error {
		snap, err := getDoc(ctx, tx.tx, d)
		if err != nil && status.Code(err) != codes.NotFound {
			return err
		}
		if snap.Exists() {
			switch {
			case snap.UpdateTime.Equal(updated.Truncate(time.Microsecond)):
				result = Unchanged
				return nil
			case snap.UpdateTime.After(updated):
				mergeAll(m, snap.m)
				result = Merged
				updated = snap.UpdateTime
			}
			created = snap.CreateTime
		}
		raw, err := encodeDoc(m)
		if err != nil {
			return err
		}
		_, err = tx.tx.ExecContext(ctx, `INSERT INTO docs (path, parent, id, data, create_time, update_time) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(path) DO UPDATE SET data = excluded.data, create_time = excluded.create_time, update_time = excluded.update_time`,
			d.Path, d.Parent.Path, d.ID, raw, created.UnixMicro(), updated.UnixMicro())
		return err
	})
	return result, err
}

// CountByCollection returns the number of documents per collection path
// with ids stripped ("users/*/pipeline_runs"), for the migration's check.
func (c *Client) CountByCollection(ctx context.Context) (map[string]int, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT parent, COUNT(*) FROM docs GROUP BY parent`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var parent string
		var n int
		if err := rows.Scan(&parent, &n); err != nil {
			return nil, err
		}
		out[CollectionKind(parent)] += n
	}
	return out, rows.Err()
}

// CollectionKind replaces the document ids in a collection path with '*'.
func CollectionKind(collPath string) string {
	parts := strings.Split(collPath, "/")
	for i := 1; i < len(parts); i += 2 {
		parts[i] = "*"
	}
	return strings.Join(parts, "/")
}

// Snapshot writes a consistent copy of the whole SQLite file (documents and
// queue) to path with VACUUM INTO, via a temporary file renamed into place, so
// a backup never picks up a half-written copy.
func (c *Client) Snapshot(ctx context.Context, path string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return 0, err
	}
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	if _, err := c.db.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("docstore: snapshot: %w", err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return 0, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

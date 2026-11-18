package main

// fitgluectl migrate-firestore: copy every Firestore document (every
// collection, every sub-collection) into the on-box document store
// (pkg/docstore, the docs table in FITGLUE_DB), then compare counts per
// collection. Read only on the Firestore side.
//
// Safe to re-run: documents already copied at the same version are skipped,
// and a document changed on the box since (newer update time) keeps its box
// fields, with Firestore's filling in around them. That lets the copy run
// once before the services move over and once more right after, to catch
// anything written to Firestore in between.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"

	gfs "cloud.google.com/go/firestore"
	"google.golang.org/genproto/googleapis/type/latlng"

	"github.com/fitglue/server/src/go/pkg/docstore"
	"github.com/fitglue/server/src/go/pkg/infrastructure/localqueue"
)

type migrateStats struct {
	mu        sync.Mutex
	source    map[string]int // collection kind → documents in Firestore
	imported  int
	merged    int
	unchanged int
	converted int // geo points / references turned into plain values
	failed    []string
}

func (s *migrateStats) add(kind string, r docstore.ImportResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.source[kind]++
	switch r {
	case docstore.Imported:
		s.imported++
	case docstore.Merged:
		s.merged++
	case docstore.Unchanged:
		s.unchanged++
	}
}

func (s *migrateStats) fail(path string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed = append(s.failed, fmt.Sprintf("%s: %v", path, err))
}

func migrateFirestoreCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fl := flag.NewFlagSet("migrate-firestore", flag.ContinueOnError)
	fl.SetOutput(stderr)
	project := fl.String("project", envOr("GOOGLE_CLOUD_PROJECT", os.Getenv("PROJECT_ID")), "GCP project id")
	dbPath := fl.String("db", localqueue.DefaultPath(), "SQLite file to copy into")
	apply := fl.Bool("apply", false, "write the documents (default: count only)")
	workers := fl.Int("workers", 8, "parallel Firestore readers")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *project == "" {
		return fmt.Errorf("no project: set GOOGLE_CLOUD_PROJECT or pass --project")
	}
	src, err := gfs.NewClient(ctx, *project)
	if err != nil {
		return fmt.Errorf("firestore: %w", err)
	}
	defer src.Close()
	dst, err := docstore.Open(*dbPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	stats := &migrateStats{source: map[string]int{}}
	sem := make(chan struct{}, max(*workers, 1))
	var wg sync.WaitGroup
	var walkColl func(col *gfs.CollectionRef, rel string)
	walkDoc := func(ref *gfs.DocumentRef, rel string) {
		defer wg.Done()
		sem <- struct{}{}
		subs, err := ref.Collections(ctx).GetAll()
		<-sem
		if err != nil {
			stats.fail(rel, fmt.Errorf("list sub-collections: %w", err))
			return
		}
		for _, sc := range subs {
			walkColl(sc, rel+"/"+sc.ID)
		}
	}
	walkColl = func(col *gfs.CollectionRef, rel string) {
		kind := docstore.CollectionKind(rel)
		sem <- struct{}{}
		snaps, err := readPaged(ctx, col)
		var refs []*gfs.DocumentRef
		if err == nil {
			// DocumentRefs also lists "missing" documents that exist only as
			// the parent of a sub-collection.
			refs, err = col.DocumentRefs(ctx).GetAll()
		}
		<-sem
		if err != nil {
			stats.fail(rel, err)
			return
		}
		for _, snap := range snaps {
			path := rel + "/" + snap.Ref.ID
			data, n := plainFirestoreValue(snap.Data())
			if n > 0 {
				stats.mu.Lock()
				stats.converted += n
				stats.mu.Unlock()
			}
			r := docstore.Unchanged
			if *apply {
				r, err = dst.Import(ctx, path, data.(map[string]interface{}), snap.CreateTime, snap.UpdateTime)
				if err != nil {
					stats.fail(path, err)
					continue
				}
			}
			stats.add(kind, r)
		}
		for _, ref := range refs {
			wg.Add(1)
			go walkDoc(ref, rel+"/"+ref.ID)
		}
	}

	tops, err := src.Collections(ctx).GetAll()
	if err != nil {
		return fmt.Errorf("list collections: %w", err)
	}
	for _, col := range tops {
		walkColl(col, col.ID)
	}
	wg.Wait()

	local, err := dst.CountByCollection(ctx)
	if err != nil {
		return err
	}
	kinds := map[string]bool{}
	for k := range stats.source {
		kinds[k] = true
	}
	for k := range local {
		kinds[k] = true
	}
	var names []string
	for k := range kinds {
		names = append(names, k)
	}
	sort.Strings(names)
	mismatch := 0
	fmt.Fprintf(stdout, "%-48s %9s %9s\n", "collection", "firestore", "box")
	quiet := 0
	for _, k := range names {
		if local[k] == stats.source[k] && local[k] < 10 {
			quiet++ // small matching collections (e.g. per-activity GitHub paths)
			continue
		}
		mark := ""
		if *apply && local[k] < stats.source[k] {
			mark = "  MISSING"
			mismatch++
		} else if local[k] > stats.source[k] {
			mark = "  (box only)"
		}
		fmt.Fprintf(stdout, "%-48s %9d %9d%s\n", k, stats.source[k], local[k], mark)
	}
	if quiet > 0 {
		fmt.Fprintf(stdout, "(+ %d smaller collections, counts match)\n", quiet)
	}
	total := 0
	for _, n := range stats.source {
		total += n
	}
	mode := "dry run (no writes; --apply to copy)"
	if *apply {
		mode = fmt.Sprintf("copied %d, merged %d (changed on the box since), unchanged %d", stats.imported, stats.merged, stats.unchanged)
	}
	fmt.Fprintf(stdout, "\n%d documents in Firestore; %s\n", total, mode)
	if stats.converted > 0 {
		fmt.Fprintf(stdout, "%d geo point / reference values stored as plain maps / paths\n", stats.converted)
	}
	if len(stats.failed) > 0 {
		fmt.Fprintf(stdout, "%d failures:\n  %s\n", len(stats.failed), strings.Join(stats.failed, "\n  "))
		return fmt.Errorf("%d documents or collections failed", len(stats.failed))
	}
	if mismatch > 0 {
		return fmt.Errorf("%d collections have fewer documents on the box than in Firestore", mismatch)
	}
	return nil
}

// plainFirestoreValue turns the two Firestore-only value types into plain
// ones docstore stores: a geo point becomes {latitude, longitude}, a document
// reference its path relative to the database root. It returns how many it
// converted.
func plainFirestoreValue(v interface{}) (interface{}, int) {
	switch x := v.(type) {
	case *latlng.LatLng:
		if x == nil {
			return nil, 0
		}
		return map[string]interface{}{"latitude": x.Latitude, "longitude": x.Longitude}, 1
	case *gfs.DocumentRef:
		if x == nil {
			return nil, 0
		}
		p := x.Path
		if i := strings.Index(p, "/documents/"); i >= 0 {
			p = p[i+len("/documents/"):]
		}
		return p, 1
	case map[string]interface{}:
		n := 0
		out := make(map[string]interface{}, len(x))
		for k, e := range x {
			var c int
			out[k], c = plainFirestoreValue(e)
			n += c
		}
		return out, n
	case []interface{}:
		n := 0
		out := make([]interface{}, len(x))
		for i, e := range x {
			var c int
			out[i], c = plainFirestoreValue(e)
			n += c
		}
		return out, n
	}
	return v, 0
}

// readPaged reads a collection 300 documents at a time in id order: one
// query over a large collection (users/*/executions) times out server-side.
func readPaged(ctx context.Context, col *gfs.CollectionRef) ([]*gfs.DocumentSnapshot, error) {
	const page = 300
	var out []*gfs.DocumentSnapshot
	q := col.OrderBy(gfs.DocumentID, gfs.Asc).Limit(page)
	for {
		snaps, err := q.Documents(ctx).GetAll()
		if err != nil {
			return nil, err
		}
		out = append(out, snaps...)
		if len(snaps) < page {
			return out, nil
		}
		q = col.OrderBy(gfs.DocumentID, gfs.Asc).StartAfter(snaps[len(snaps)-1].Ref.ID).Limit(page)
	}
}

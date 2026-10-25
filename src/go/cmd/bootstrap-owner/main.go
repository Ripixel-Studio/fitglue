// bootstrap-owner upserts the single owner's user document in Firestore.
//
// FitGlue is single-user. The public sign-up path that used to create a user
// document on first Firebase login is gone, so the owner's document is created
// (or repaired) once, by hand, with this tool:
//
//	make bootstrap-owner UID=<firebase uid> EMAIL=<email> [DISPLAY_NAME="James"]
//
// It is idempotent: an existing document keeps every field and subcollection
// (integrations, counters, personal records, plugin defaults) and only has the
// identity fields merged in. It talks to Firestore with Application Default
// Credentials against GOOGLE_CLOUD_PROJECT (or PROJECT_ID, or -project).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	cloudFirestore "cloud.google.com/go/firestore"
	"github.com/fitglue/server/src/go/internal/user"
)

func main() {
	uid := flag.String("uid", "", "owner's user id (the Firebase Auth uid the existing document is keyed by)")
	email := flag.String("email", "", "owner's email address")
	displayName := flag.String("display-name", "", "owner's display name (optional)")
	project := flag.String("project", "", "GCP project id (defaults to GOOGLE_CLOUD_PROJECT / PROJECT_ID)")
	flag.Parse()

	if *uid == "" || *email == "" {
		fmt.Fprintln(os.Stderr, "usage: bootstrap-owner -uid <firebase uid> -email <email> [-display-name <name>] [-project <gcp project>]")
		os.Exit(2)
	}

	projectID := *project
	if projectID == "" {
		projectID = os.Getenv("GOOGLE_CLOUD_PROJECT")
	}
	if projectID == "" {
		projectID = os.Getenv("PROJECT_ID")
	}
	if projectID == "" {
		fmt.Fprintln(os.Stderr, "bootstrap-owner: no project id — set GOOGLE_CLOUD_PROJECT or pass -project")
		os.Exit(2)
	}

	ctx := context.Background()
	client, err := cloudFirestore.NewClient(ctx, projectID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap-owner: firestore client: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	created, err := user.NewFirestoreStore(client).BootstrapOwner(ctx, *uid, *email, *displayName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap-owner: %v\n", err)
		os.Exit(1)
	}
	if created {
		fmt.Printf("created users/%s in %s (%s)\n", *uid, projectID, *email)
	} else {
		fmt.Printf("users/%s already exists in %s — identity fields merged (%s)\n", *uid, projectID, *email)
	}
}

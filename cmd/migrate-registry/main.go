// Command migrate-registry copies the stable application catalog from the
// legacy SQLite database to a configured MySQL schema. It is non-destructive:
// the source is read-only from the command's perspective and destination rows
// are upserted by their natural keys.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"logplatform/internal/platform"
)

func main() {
	source := flag.String("source", "./runtime/platform-data/registry.db", "source SQLite registry.db")
	targetDriver := flag.String("target-driver", "mysql", "target database driver")
	targetDSN := flag.String("target-dsn", os.Getenv("DEVOPSPLATFORM_DB_DSN"), "target DSN (prefer DEVOPSPLATFORM_DB_DSN)")
	flag.Parse()

	if *targetDSN == "" {
		log.Fatal("target DSN is required: set DEVOPSPLATFORM_DB_DSN or -target-dsn")
	}
	src, err := platform.OpenRegistry(*source)
	if err != nil {
		log.Fatalf("open source catalog: %v", err)
	}
	defer src.Close()
	snapshot, err := src.Snapshot()
	if err != nil {
		log.Fatalf("read source catalog: %v", err)
	}

	dst, err := platform.OpenRegistryDatabase(platform.RegistryConfig{Driver: *targetDriver, DSN: *targetDSN})
	if err != nil {
		log.Fatalf("open target catalog: %v", err)
	}
	defer dst.Close()
	if err := dst.Restore(snapshot); err != nil {
		log.Fatalf("copy catalog: %v", err)
	}
	verify, err := dst.Snapshot()
	if err != nil {
		log.Fatalf("verify target catalog: %v", err)
	}
	if len(verify.UserIdentities) < len(snapshot.UserIdentities) || len(verify.Services) < len(snapshot.Services) || len(verify.Deployments) < len(snapshot.Deployments) || len(verify.Identities) < len(snapshot.Identities) || len(verify.Users) < len(snapshot.Users) {
		log.Fatalf("verification failed: source=%d/%d/%d/%d/%d target=%d/%d/%d/%d/%d",
			len(snapshot.Services), len(snapshot.Deployments), len(snapshot.Identities), len(snapshot.Users), len(snapshot.UserIdentities),
			len(verify.Services), len(verify.Deployments), len(verify.Identities), len(verify.Users), len(verify.UserIdentities))
	}
	fmt.Printf("catalog migration complete: applications=%d deployments=%d identities=%d users=%d user_identities=%d\n",
		len(snapshot.Services), len(snapshot.Deployments), len(snapshot.Identities), len(snapshot.Users), len(snapshot.UserIdentities))
}

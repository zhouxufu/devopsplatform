package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"logplatform/internal/monitor"
)

func main() {
	source := flag.String("source", "", "source SQLite metrics.db path")
	targetDSN := flag.String("target-dsn", os.Getenv("DEVOPSPLATFORM_MONITOR_DB_DSN"), "target MySQL DSN (prefer DEVOPSPLATFORM_MONITOR_DB_DSN)")
	flag.Parse()
	if *source == "" || *targetDSN == "" {
		flag.Usage()
		os.Exit(2)
	}
	report, err := monitor.MigrateSQLiteToMySQL(*source, *targetDSN)
	if err != nil {
		log.Fatal(err)
	}
	for _, table := range report.SortedMigrationTables() {
		fmt.Printf("%-28s %d\n", table, report.Tables[table])
	}
	fmt.Printf("migration completed: %d rows\n", report.Total)
}

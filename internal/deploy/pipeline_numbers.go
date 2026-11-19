package deploy

import "fmt"

// Display numbers are application-local. Global run IDs remain the immutable
// keys used by routes, artifacts, approvals, snapshots and execution controls.
func (s *Store) initPipelineRunNumbers() error {
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS pipeline_run_counters (service VARCHAR(255) PRIMARY KEY,last_number BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS pipeline_run_numbers (run_id BIGINT PRIMARY KEY,service VARCHAR(255) NOT NULL,number BIGINT NOT NULL,UNIQUE(service,number))`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	for {
		rows, err := s.db.Query(`SELECT id,COALESCE(service,'') FROM pipeline_runs WHERE NOT EXISTS (SELECT 1 FROM pipeline_run_numbers n WHERE n.run_id=pipeline_runs.id) ORDER BY id LIMIT 500`)
		if err != nil {
			return err
		}
		type oldRun struct {
			id      int64
			service string
		}
		var pending []oldRun
		for rows.Next() {
			var r oldRun
			if err = rows.Scan(&r.id, &r.service); err != nil {
				rows.Close()
				return err
			}
			pending = append(pending, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			return nil
		}
		for _, r := range pending {
			tx, err := s.db.Begin()
			if err != nil {
				return err
			}
			if err = lockPipelineCounter(tx, r.service); err != nil {
				tx.Rollback()
				return err
			}
			var exists int
			if err = tx.QueryRow(`SELECT COUNT(*) FROM pipeline_run_numbers WHERE run_id=?`, r.id).Scan(&exists); err != nil {
				tx.Rollback()
				return err
			}
			if exists == 0 {
				number, e := advancePipelineCounter(tx, r.service)
				if e != nil {
					tx.Rollback()
					return e
				}
				if _, err = tx.Exec(`INSERT INTO pipeline_run_numbers(run_id,service,number) VALUES(?,?,?)`, r.id, r.service, number); err != nil {
					tx.Rollback()
					return err
				}
			}
			if err = tx.Commit(); err != nil {
				return err
			}
		}
	}
}

// The upsert acquires a write/row lock before any reads, on both SQLite and
// MySQL. Counter allocation and run creation must share this transaction.
func lockPipelineCounter(tx *deployTx, service string) error {
	_, err := tx.Exec(`INSERT INTO pipeline_run_counters(service,last_number) VALUES(?,0) ON CONFLICT(service) DO UPDATE SET service=excluded.service`, service)
	return err
}
func advancePipelineCounter(tx *deployTx, service string) (int64, error) {
	if _, err := tx.Exec(`UPDATE pipeline_run_counters SET last_number=last_number+1 WHERE service=?`, service); err != nil {
		return 0, err
	}
	var number int64
	err := tx.QueryRow(`SELECT last_number FROM pipeline_run_counters WHERE service=?`, service).Scan(&number)
	return number, err
}
func (s *Store) PipelineRunNumber(id int64, service string) int64 {
	var n int64
	_ = s.db.QueryRow(`SELECT number FROM pipeline_run_numbers WHERE run_id=? AND service=?`, id, service).Scan(&n)
	return n
}
func (s *Store) PipelineRunLabel(id int64, service string) string {
	if number := s.PipelineRunNumber(id, service); number > 0 {
		return fmt.Sprintf("#%d", number)
	}
	return fmt.Sprintf("记录 ID %d", id)
}

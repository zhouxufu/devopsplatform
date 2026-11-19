package deploy

import "fmt"

// Display numbers are application-local. Global release IDs remain the immutable
// keys used by routes, artifacts, approvals, snapshots and execution controls.
func (s *Store) initReleaseOrderNumbers() error {
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS release_order_counters (service VARCHAR(255) PRIMARY KEY,last_number BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS release_order_numbers (release_id BIGINT PRIMARY KEY,service VARCHAR(255) NOT NULL,number BIGINT NOT NULL,UNIQUE(service,number))`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	for {
		rows, err := s.db.Query(`SELECT id,COALESCE(service,'') FROM release_orders WHERE NOT EXISTS (SELECT 1 FROM release_order_numbers n WHERE n.release_id=release_orders.id) ORDER BY id LIMIT 500`)
		if err != nil {
			return err
		}
		type oldRelease struct {
			id      int64
			service string
		}
		var pending []oldRelease
		for rows.Next() {
			var r oldRelease
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
			if err = lockReleaseCounter(tx, r.service); err != nil {
				tx.Rollback()
				return err
			}
			var exists int
			if err = tx.QueryRow(`SELECT COUNT(*) FROM release_order_numbers WHERE release_id=?`, r.id).Scan(&exists); err != nil {
				tx.Rollback()
				return err
			}
			if exists == 0 {
				number, e := advanceReleaseCounter(tx, r.service)
				if e != nil {
					tx.Rollback()
					return e
				}
				if _, err = tx.Exec(`INSERT INTO release_order_numbers(release_id,service,number) VALUES(?,?,?)`, r.id, r.service, number); err != nil {
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
// MySQL. Counter allocation and release creation must share this transaction.
func lockReleaseCounter(tx *deployTx, service string) error {
	_, err := tx.Exec(`INSERT INTO release_order_counters(service,last_number) VALUES(?,0) ON CONFLICT(service) DO UPDATE SET service=excluded.service`, service)
	return err
}
func advanceReleaseCounter(tx *deployTx, service string) (int64, error) {
	if _, err := tx.Exec(`UPDATE release_order_counters SET last_number=last_number+1 WHERE service=?`, service); err != nil {
		return 0, err
	}
	var number int64
	err := tx.QueryRow(`SELECT last_number FROM release_order_counters WHERE service=?`, service).Scan(&number)
	return number, err
}
func (s *Store) ReleaseOrderNumber(id int64, service string) int64 {
	var n int64
	_ = s.db.QueryRow(`SELECT number FROM release_order_numbers WHERE release_id=? AND service=?`, id, service).Scan(&n)
	return n
}
func (s *Store) ReleaseOrderLabel(id int64, service string) string {
	if number := s.ReleaseOrderNumber(id, service); number > 0 {
		return fmt.Sprintf("#%d", number)
	}
	return fmt.Sprintf("记录 ID %d", id)
}

// DisplayLabel never performs a query, including while a cursor/transaction is open.
func (o *ReleaseOrder) DisplayLabel() string {
	if o.Number > 0 {
		return fmt.Sprintf("#%d", o.Number)
	}
	return fmt.Sprintf("记录 ID %d", o.ID)
}

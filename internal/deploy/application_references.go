package deploy

import "logplatform/internal/appcatalog"

func (s *Store) applicationReferences() ([]appcatalog.ApplicationReference, error) {
	rows, err := s.db.Query(`SELECT name,COALESCE(application_id,'') FROM services ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []appcatalog.ApplicationReference{}
	for rows.Next() {
		var ref appcatalog.ApplicationReference
		if err := rows.Scan(&ref.Name, &ref.ApplicationID); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

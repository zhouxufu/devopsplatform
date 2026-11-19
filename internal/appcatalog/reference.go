package appcatalog

// ApplicationReference contains identity only, never editable configuration.
type ApplicationReference struct {
	ApplicationID string `json:"application_id"`
	Name          string `json:"name"`
}

// Legacy aliases are identifiers, never presentation labels. Retain a
// conflicting name conservatively rather than removing a live association.
func HasApplicationReference(refs []ApplicationReference, id string, names []string) bool {
	for _, ref := range refs {
		if id != "" && ref.ApplicationID == id {
			return true
		}
		for _, name := range names {
			if name != "" && ref.Name == name {
				return true
			}
		}
	}
	return false
}

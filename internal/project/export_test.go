package project

type ProjectHandleForTest = projectHandle

func NewProjectHandleForTest(record ProjectRecord) *projectHandle {
	return &projectHandle{record: record}
}

type RegistryForTest struct {
	registry *projectRegistry
}

func NewRegistryForTest() (*RegistryForTest, error) {
	store, err := newRegistryStore()
	if err != nil {
		return nil, err
	}
	return &RegistryForTest{registry: newRegistry(store)}, nil
}

func (r *RegistryForTest) RemoveByRoot(root string) error {
	return r.registry.RemoveByRoot(root)
}

func (r *RegistryForTest) Projects() []ProjectEntry {
	return r.registry.Projects()
}

func (r *RegistryForTest) Update(entry ProjectEntry) (ProjectEntry, error) {
	return r.registry.Update(entry)
}

func (r *RegistryForTest) Save() error {
	return r.registry.Save()
}

func (r *RegistryForTest) ProjectByRoot(root string) (ProjectEntry, bool, error) {
	return r.registry.ProjectByRoot(root)
}

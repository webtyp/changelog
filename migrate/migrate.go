package migrate

import (
	"webtyp.com/changelog"
	"webtyp.com/ddl"
)

func Migrate(conn ddl.Execer, compiler ddl.Compiler) error {
	m := ddl.New(conn, compiler)
	if err := m.CreateTable(&changelog.Change{}); err != nil {
		return err
	}
	return nil
}

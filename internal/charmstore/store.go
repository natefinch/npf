package charmstore

import (
	"labix.org/v2/mgo"
)

type Store struct {
	db *mgo.Database
}

func newStore(db *mgo.Database) *Store {
	return &Store{
		db: db,
	}
}

func (s *Store) DB() *mgo.Database {
	return s.db
}

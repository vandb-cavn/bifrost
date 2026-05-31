package configstore

import (
	"gorm.io/gorm"
)

// NewRDBConfigStoreForTest returns an RDBConfigStore over an existing *gorm.DB.
// The database must already be migrated for the tables the test exercises.
func NewRDBConfigStoreForTest(db *gorm.DB) *RDBConfigStore {
	if db == nil {
		panic("configstore: NewRDBConfigStoreForTest(nil)")
	}
	s := &RDBConfigStore{logger: nil}
	s.db.Store(db)
	return s
}

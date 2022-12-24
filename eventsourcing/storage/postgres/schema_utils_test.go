package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSchemaUtils(t *testing.T) {
	ctx := context.TODO()
	db, err := dbHandle(ctx)
	assert.NoError(t, err)

	// create the tables
	schemaUtils := NewSchemaUtils(db)
	err = schemaUtils.CreateTable(ctx)
	assert.NoError(t, err)

	// assert existence of the table
	err = db.TableExists(ctx, "event_journal")
	assert.NoError(t, err)

	err = db.TableExists(ctx, "event_journal")
	assert.NoError(t, err)

	// clean up
	assert.NoError(t, schemaUtils.DropTable(ctx))
	assert.NoError(t, db.Disconnect(ctx))
}

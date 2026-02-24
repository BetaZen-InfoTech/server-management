package agent

import (
	"context"
	"fmt"
)

func CreateMongoDatabase(ctx context.Context, dbName, username, password string) error {
	cmd := fmt.Sprintf(`mongosh --quiet --eval 'use %s; db.createUser({user: "%s", pwd: "%s", roles: [{role: "readWrite", db: "%s"}]})'`, dbName, username, password, dbName)
	_, err := RunCommand(ctx, "bash", "-c", cmd)
	return err
}

func DeleteMongoDatabase(ctx context.Context, dbName string) error {
	cmd := fmt.Sprintf(`mongosh --quiet --eval 'use %s; db.dropDatabase()'`, dbName)
	_, err := RunCommand(ctx, "bash", "-c", cmd)
	return err
}

func CreateMongoUser(ctx context.Context, dbName, username, password, role string) error {
	cmd := fmt.Sprintf(`mongosh --quiet --eval 'use %s; db.createUser({user: "%s", pwd: "%s", roles: [{role: "%s", db: "%s"}]})'`, dbName, username, password, role, dbName)
	_, err := RunCommand(ctx, "bash", "-c", cmd)
	return err
}

func DeleteMongoUser(ctx context.Context, dbName, username string) error {
	cmd := fmt.Sprintf(`mongosh --quiet --eval 'use %s; db.dropUser("%s")'`, dbName, username)
	_, err := RunCommand(ctx, "bash", "-c", cmd)
	return err
}

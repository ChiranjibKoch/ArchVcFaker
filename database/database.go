package database

import (
	"context"
	"fmt"
	"sync"
	"time"

	"log"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"tgmultibot/utils"
)

var (
	client        *mongo.Client
	database      *mongo.Database
	settingsColl  *mongo.Collection
	userStateColl *mongo.Collection

	dbCache        = utils.NewCache[string, any](60 * time.Minute)
	userStateCache = utils.NewCache[int64, *UserState](60 * time.Minute)
)

func Load(mongoURL, dbName string) (func(), error) {
	var err error
	log.Println("Initializing MongoDB...")
	client, err = mongo.Connect(options.Client().ApplyURI(mongoURL))
	if err != nil {
		return nil, err
	}

	log.Println("Successfully connected to MongoDB.")

	database = client.Database(dbName)
	settingsColl = database.Collection("bot_settings")
	userStateColl = database.Collection("user_settings")

	if err := migrateLegacyTokens(); err != nil {
		return nil, fmt.Errorf("failed to migrate legacy tokens: %w", err)
	}
	if err := buildReverseIndexes(); err != nil {
		return nil, fmt.Errorf("failed to build reverse indexes: %w", err)
	}

	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Disconnect(ctx); err != nil {
			log.Printf("Error while disconnecting MongoDB: %v", err)
		} else {
			log.Println("MongoDB disconnected successfully")
		}
	}, nil
}

func GetMongoDBStats() (bson.M, error) {
	ctx, cancel := ctx()
	defer cancel()
	var result bson.M
	err := database.RunCommand(ctx, bson.D{{Key: "dbStats", Value: 1}}).Decode(&result)
	return result, err
}

// AllUsersWithClients returns every UserState that has at least one client stored.
// Used on startup to re-connect all persisted sessions.
func AllUsersWithClients() ([]*UserState, error) {
	c, cancel := ctx()
	defer cancel()

	filter := bson.M{"cli": bson.M{"$exists": true, "$not": bson.M{"$size": 0}}}
	cursor, err := userStateColl.Find(c, filter, options.Find())
	if err != nil {
		return nil, fmt.Errorf("AllUsersWithClients: %w", err)
	}
	defer cursor.Close(c)

	var states []*UserState
	for cursor.Next(c) {
		var s UserState
		if err := cursor.Decode(&s); err != nil {
			continue
		}
		buildClientIndex(&s)
		if err := decodeClients(&s); err != nil {
			continue
		}

		states = append(states, &s)
	}
	return states, cursor.Err()
}

// reverse indexes: chatID -> []userID, colocated with UserState since
// they index UserState.AutoReactChats / AutoViewChats across all users.
var (
	reactOwnersMu sync.RWMutex
	reactOwners   = map[int64][]int64{}

	viewOwnersMu sync.RWMutex
	viewOwners   = map[int64][]int64{}

	// delegateGrantorsMu/delegateGrantors index UserState.Delegates: for a
	// given delegate (someone who redeemed an access token), which
	// grantor(s) currently trust them. Colocated here for the same reason
	// as reactOwners/viewOwners above.
	delegateGrantorsMu sync.RWMutex
	delegateGrantors   = map[int64][]int64{}
)

// buildReverseIndexes loads every UserState once and populates the
// in-memory chatID -> []userID maps. Call once at startup from Load().
func buildReverseIndexes() error {
	ctx, cancel := ctx()
	defer cancel()

	cur, err := userStateColl.Find(ctx, bson.M{})
	if err != nil {
		return err
	}
	defer cur.Close(ctx)

	for cur.Next(ctx) {
		var s UserState
		if err := cur.Decode(&s); err != nil {
			return err
		}
		for _, chatID := range s.AutoReactChats {
			reactOwners[chatID], _ = addUnique(reactOwners[chatID], s.UserID)
		}
		for _, chatID := range s.AutoViewChats {
			viewOwners[chatID], _ = addUnique(viewOwners[chatID], s.UserID)
		}
		for _, delegateID := range s.Delegates {
			delegateGrantors[delegateID], _ = addUnique(delegateGrantors[delegateID], s.UserID)
		}
	}
	return cur.Err()
}

func migrateLegacyTokens() error {
	ctx, cancel := ctx()
	defer cancel()

	// $type 4 = BSON array
	filter := bson.M{"tok": bson.M{"$type": 4}}
	update := bson.M{"$set": bson.M{"tok": ""}}

	res, err := userStateColl.UpdateMany(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("migrateLegacyTokens: %w", err)
	}
	if res.ModifiedCount > 0 {
		log.Printf("migrateLegacyTokens: cleared legacy tok array from %d documents", res.ModifiedCount)
	}
	return nil
}

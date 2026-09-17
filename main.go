package main

import (
	"fmt"
	"log"

	"tgmultibot/config"
	"tgmultibot/database"
	"tgmultibot/manager"
	"tgmultibot/modules"

	"github.com/amarnathcjd/gogram/telegram"
)

func main() {
	if err := config.Load(); err != nil {
		log.Fatal(err)
	}

	dbClose, err := database.Load(config.MongoURL, config.DbName)
	if err != nil {
		log.Fatal("failed to connect to MongoDB:", err)
	}
	defer dbClose()

	cm := manager.NewClientManager(int32(config.ApiID), config.ApiHash)

	client, err := telegram.NewClient(telegram.ClientConfig{
		AppID:    int32(config.ApiID),
		AppHash:  config.ApiHash,
		LogLevel: telegram.LogInfo,
	})
	if err != nil {
		log.Fatal("failed to create client:", err)
	}

	if err := client.LoginBot(config.Token); err != nil {
		log.Fatal("failed to login bot:", err)
	}

	if err := config.ResolveOwner(client); err != nil {
		log.Fatal("failed to resolve bot owner:", err)
	}

	if err := modules.Load(client, cm); err != nil {
		log.Fatal("failed to load modules:", err)
	}

	me, err := client.GetMe()
	if err != nil {
		log.Fatal("failed to get bot info:", err)
	}

	if config.LoggerID != 0 {
		client.SendMessage(
			config.LoggerID,
			fmt.Sprintf(
				"✅ <b>Bot Started</b>\n\n👤 <b>Name:</b> %s\n🤖 <b>Username:</b> @%s\n🆔 <b>ID:</b> <code>%d</code>",
				me.FirstName,
				me.Username,
				me.ID,
			),
			&telegram.SendOptions{ParseMode: telegram.HTML},
		)
	}

	cm.RestoreFromDB(func(msg string) {
		if config.LoggerID != 0 {
			client.SendMessage(config.LoggerID, msg, &telegram.SendOptions{ParseMode: telegram.HTML})
		}

	})

	log.Printf("Bot started as @%s", me.Username)
	client.Idle()
}

package main

import (
	// "encoding/binary"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/jonas747/dca"
)

var Token string

func init() {
	flag.StringVar(&Token, "t", "", "Bot Token")
	flag.Parse()
}

func main() {
	dg, err := discordgo.New("Bot " + Token)
	if err != nil {
		fmt.Println("error creating Discord session,", err)
		return
	}

	dg.AddHandler(messageCreate)

	// In this example, we only care about receiving message events.
	dg.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentsGuildVoiceStates

	if dg.Open() != nil {
		fmt.Println("error opening connection,", err)
		return
	}
	fmt.Println("Bot is now running.  Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc

	dg.Close()
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}
	if strings.HasPrefix(m.Content, "!play") {
		// Find the channel that the message came from.
		c, err := s.State.Channel(m.ChannelID)
		if err != nil {
			// Could not find channel.
			return
		}

		// Find the guild for that channel.
		g, err := s.State.Guild(c.GuildID)
		if err != nil {
			// Could not find guild.
			return
		}

		// Look for the message sender in that guild's current voice states.
		for _, vs := range g.VoiceStates {
			if vs.UserID == m.Author.ID {
				err = playSound(s, "/home/qiu/music/k.mp3", g.ID, vs.ChannelID)
				s.ChannelMessageSend(m.ChannelID, "Plaing!")
				if err != nil {
					fmt.Println("Error playing sound:", err)
				}
				return
			}
		}
	}
}

func playSound(s *discordgo.Session, path, guildID, channelID string) (err error) {

	encodeSession, err := dca.EncodeFile(path, dca.StdEncodeOptions)
	if err != nil {
		    fmt.Println("file not found")
	}
	defer encodeSession.Cleanup()

	vc, err := s.ChannelVoiceJoin(guildID, channelID, false, true)
	if err != nil {
		return err
	}
	time.Sleep(30 * time.Millisecond)
	vc.Speaking(true)

	done := make(chan error)    
	dca.NewStream(encodeSession, vc, done)
	erro := <- done
	if erro != nil && erro != io.EOF {
		s.ChannelMessageSend(channelID, "shit happens")
	}

	// // Send the buffer data.
	// for _, buff := range buffer {
		// vc.OpusSend <- buff
	// }

	vc.Speaking(false)
	time.Sleep(250 * time.Millisecond)
	vc.Disconnect()

	return nil
}

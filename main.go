package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"path/filepath"
	"math/rand"

	"github.com/bwmarrin/discordgo"
	"github.com/jonas747/dca"
)

var Token string
var DirLocaton string
var isPlaying bool = false
var done chan error

func init() {
	flag.StringVar(&Token, "t", "", "Bot Token")
	flag.StringVar(&DirLocaton, "d", "", "Path to Music (Directory)")
	flag.Parse()
	rand.Seed(time.Now().UnixNano())
}

func main() {
	dg, err := discordgo.New("Bot " + Token)
	if err != nil {
		fmt.Println("error creating Discord session,", err)
		return
	}
	dg.LogLevel = discordgo.LogWarning
	dg.AddHandler(messageCreate)
	dg.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentsGuildVoiceStates
	if err = dg.Open(); err != nil {
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
	if strings.HasPrefix(m.Content, "!play") && isPlaying == false{
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
				s.ChannelMessageSend(m.ChannelID, "Playing!")
				err = playSound(s, DirLocaton, g.ID, vs.ChannelID)
				if err != nil {
					fmt.Println("Error playing sound:", err)
				}
				return
			}
		}
	}
	if strings.HasPrefix(m.Content, "!skip") {
		if isPlaying == false {
			s.ChannelMessageSend(m.ChannelID, "There is nothing to skip. Noob...")
			return
		}
		s.ChannelMessageSend(m.ChannelID, "Skipping...")
		done <- io.EOF

		
	}
}

func playSound(s *discordgo.Session, path, guildID, channelID string) (err error) {
	isPlaying = true
	vc, err := s.ChannelVoiceJoin(guildID, channelID, false, true)
	var music []string
	err = filepath.Walk(path,
		func(path string, info os.FileInfo, err error) error {
		if err != nil {
		    return err
		}
		if info.IsDir() == false {
			music = append(music, path)
		}
		return nil
	})
	if err != nil {
	    fmt.Println(err)
	}
	for i := range music {
		j := rand.Intn(i + 1)
		music[i], music[j] = music[j], music[i]
	}

	for _, music_path := range music {
		encodeSession, err := dca.EncodeFile(music_path, dca.StdEncodeOptions)
		if err != nil {
			    fmt.Println("file not found")
		}
		defer encodeSession.Cleanup()

		if err != nil {
			return err
		}
		time.Sleep(30 * time.Millisecond)
		vc.Speaking(true)

		done = make(chan error)    
		streamSes := dca.NewStream(encodeSession, vc, done)
		erro := <- done
		if erro != nil && erro != io.EOF {
			streamSes.SetPaused(true)
			s.ChannelMessageSend(channelID, "shit happens")
		}
	}

	vc.Speaking(false)
	time.Sleep(250 * time.Millisecond)
	vc.Disconnect()
	isPlaying = false

	return nil
}

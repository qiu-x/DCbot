// Dependencies: youtube-dl has to be in $PATH

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"strconv"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/jonas747/dca"
)

const PREFIX = "PiS"

const HELP = "```" + `shell
Flags:

--list, -l
	Print current track queue

--skip, -s
	Skip current track

--pause, -z
	Pause current track

--resmue, -r
	Rersume current track

--quit, -q
	Disconnect bot

--come-here, -c
	Connect to current channel

--help, -h
	Print this message
` + "```"

var ytdlName string = "youtube-dl"
	
var Token string

func init() {
	flag.StringVar(&Token, "t", "", "Bot Token")
	flag.Parse()
	rand.Seed(time.Now().UnixNano())
}

type Track struct {
	url string
	name string
}

func getTrackName(url string) string {
	name, err := exec.Command(ytdlName, "-e", url).Output()
	if err != nil {
		fmt.Println(err)
		return "Could not fetch track name"
	}
	return strings.TrimSpace(string(name))
}

func dlTrack(url string) io.Reader {
	// Setup youtube-dl
	ytdl := exec.Command(ytdlName, url, "-o", "-")
	r, err := ytdl.StdoutPipe()

	if err != nil {
		fmt.Println(err)
	}
	ytdl.Start()
	fmt.Println("Downloading: " + url)
	return r
}

type DiscordData struct {
	session    *discordgo.Session
	guildID    string
	channelID  string
	mChannelID string
}
type MusicPlayer struct {
	queue    []Track
	channels struct {
		queue  chan []Track
		pause  chan bool
		skip  chan bool
		dcData chan DiscordData
		quit   chan bool
	}
	dcData DiscordData
}

var mp *MusicPlayer

func newMusicPlayer() *MusicPlayer {
	m := new(MusicPlayer)
	m.channels.queue = make(chan []Track)
	m.channels.pause = make(chan bool)
	m.channels.skip = make(chan bool)
	m.channels.dcData = make(chan DiscordData)
	m.channels.quit = make(chan bool)
	go m.run()
	return m
}

func (m *MusicPlayer) popQueue() (string, error) {
	if len(m.queue) == 0 {
		return "", errors.New("Queue is empty")
	}
	url := m.queue[0].url
	m.queue = m.queue[1:]
	return url, nil
}

func (m *MusicPlayer) nextTrack(vc *discordgo.VoiceConnection, done chan error) (
	*dca.StreamingSession, *dca.EncodeSession, error) {
	url, err := m.popQueue()
	r := dlTrack(url)
	if err != nil {
		return nil, nil, err
	}
	encSes, err := dca.EncodeMem(r, dca.StdEncodeOptions)
	if err != nil {
		return nil, nil, err
	}
	streamSes := dca.NewStream(encSes, vc, done)
	return streamSes, encSes, nil
}

func (m *MusicPlayer) connect(s *discordgo.Session, guildID, channelID, mChannelID string) {
	data := DiscordData{
		session:    s,
		guildID:    guildID,
		channelID:  channelID,
		mChannelID: mChannelID,
	}
	m.channels.dcData <- data
}

func (m *MusicPlayer) run() (err error) {
	var guildID, channelID string
	var vc *discordgo.VoiceConnection

	var streamSes *dca.StreamingSession
	var encSes *dca.EncodeSession
	done := make(chan error)
	for {
		select {
		case item := <-m.channels.queue:
			m.queue = append(m.queue, item...)
			if encSes == nil && streamSes == nil {
				streamSes, encSes, err = m.nextTrack(vc, done)
				if err != nil {
					m.dcData.session.ChannelMessageSend(m.dcData.mChannelID, "Error: " + err.Error())
					continue
				}
				continue
			}
			finished, _ := streamSes.Finished()
			if encSes != nil && finished {
				encSes.Cleanup()
				streamSes, encSes, err = m.nextTrack(vc, done)
				if err != nil {
					m.dcData.session.ChannelMessageSend(m.dcData.mChannelID, "Error: " + err.Error())
				}
			}
		case m.channels.queue <- m.queue:
		case pstate := <-m.channels.pause:
			if pstate {
				streamSes.SetPaused(true)
			} else {
				streamSes.SetPaused(false)
			}
		case <-m.channels.skip:
			finished, _ := streamSes.Finished()
			if len(m.queue) == 0 && (finished || streamSes == nil) {
				m.dcData.session.ChannelMessageSend(m.dcData.mChannelID, "Nothing to skip")
			} else {
				if encSes != nil {
					streamSes.SetPaused(true)
					encSes.Cleanup()
				}
				streamSes, encSes, err = m.nextTrack(vc, done)
				if err != nil {
					m.dcData.session.ChannelMessageSend(m.dcData.mChannelID, "Error: " + err.Error())
				}
			}
		case dcData := <-m.channels.dcData:
			m.dcData = dcData
			guildID = m.dcData.guildID
			oldID := channelID
			channelID = m.dcData.channelID
			if vc == nil {
				vc, err = m.dcData.session.ChannelVoiceJoin(guildID, channelID, false, true)
				if err != nil {
					m.dcData.session.ChannelMessageSend(m.dcData.mChannelID, "Cannot join channel: " + err.Error())
				}
			} else if oldID != channelID {
				vc.Disconnect()
				vc, err = m.dcData.session.ChannelVoiceJoin(guildID, channelID, false, true)
				if err != nil {
					m.dcData.session.ChannelMessageSend(m.dcData.mChannelID, "Cannot join channel: " + err.Error())
				}
				vc.Speaking(true)
				if encSes == nil {
					continue
				}
				if encSes.Running() {
					streamSes = dca.NewStream(encSes, vc, done)
					m.dcData.session.ChannelMessageSend(m.dcData.mChannelID, "Channel switched")
				}
			}
		case <-done:
			if len(m.queue) == 0 {
				if encSes != nil {
					encSes.Cleanup()
				}
				vc.Speaking(false)
				vc.Disconnect()
			} else if !encSes.Running() {
				if encSes != nil {
					encSes.Cleanup()
				}
				streamSes, encSes, err = m.nextTrack(vc, done)
				if err != nil {
					m.dcData.session.ChannelMessageSend(m.dcData.mChannelID, "Error: " + err.Error())
				}
			}
		case <-m.channels.quit:
			if encSes != nil {
				encSes.Cleanup()
			}
			vc.Speaking(false)
			vc.Disconnect()
		}
	}

	vc.Speaking(false)
	time.Sleep(250 * time.Millisecond)
	vc.Disconnect()
	return nil
}

func handleArgs(vs *discordgo.VoiceState, g *discordgo.Guild,
	s *discordgo.Session, m *discordgo.MessageCreate, args []string) {
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--play", "-p":
			i++
			if len(args)-1 >= i {
				mp.connect(s, g.ID, vs.ChannelID, m.ChannelID)
				item := Track{url: args[i], name: ""}
				item.name = getTrackName(args[i])
				mp.channels.queue <- []Track{item}
				s.ChannelMessageSend(m.ChannelID, "Track added to queue")
			} else {
				s.ChannelMessageSend(m.ChannelID, "An syntax error has occured")
			}
		case "--list", "-l":
			queue := <-mp.channels.queue
			if len(queue) == 0 {
				s.ChannelMessageSend(m.ChannelID, "Queue is empty")
				break
			}
			msg := "Current queue:\n"
			for i, v := range(queue) {
				numStr := strconv.Itoa(i+1)
				msg += "```" + numStr + ". " + v.name + "\n" + "```"
			}
			s.ChannelMessageSend(m.ChannelID, msg)
		case "--skip", "-s":
			mp.channels.skip <- true
			s.ChannelMessageSend(m.ChannelID, "Skipping current track...")
		case "--pause", "-z":
			mp.channels.pause <- true
			s.ChannelMessageSend(m.ChannelID, "Pausing playback")
		case "--resume", "-r":
			mp.channels.pause <- false
			s.ChannelMessageSend(m.ChannelID, "Resuming playback")
		case "--quit", "-q":
			mp.channels.quit <- true
		case "--come-here", "-c":
			mp.connect(s, g.ID, vs.ChannelID, m.ChannelID)
		case "--help", "-h":
			s.ChannelMessageSend(m.ChannelID, HELP)
		}
	}
}
func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// return if bot sent the message
	if m.Author.ID == s.State.User.ID {
		return
	}
	if !(strings.HasPrefix(m.Content, PREFIX)) {
		return
	}
	// Find the channel that the message came from.
	c, err := s.State.Channel(m.ChannelID)
	if err != nil {
		s.ChannelMessageSend(m.ChannelID, "Error:"+err.Error())
		return
	}

	// Find the guild for that channel.
	g, err := s.State.Guild(c.GuildID)
	if err != nil {
		s.ChannelMessageSend(m.ChannelID, "Error:"+err.Error())
		return
	}

	// Look for the message sender in that guild's current voice states.
	vs := func() *discordgo.VoiceState {
		for _, vs := range g.VoiceStates {
			if vs.UserID == m.Author.ID {
				return vs
			}
		}
		return &discordgo.VoiceState{}
	}()

	// Handle arguments from chat command
	handleArgs(vs, g, s, m, strings.Fields(m.Content))
}

func main() {
	mp = newMusicPlayer()
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

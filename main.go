// Dependencies: youtube-dl has to be in $PATH

package main

import (
	"flag"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/jonas747/dca"
)

const PREFIX = "PiS"

var Token string
var isPlaying bool = false

func init() {
	flag.StringVar(&Token, "t", "", "Bot Token")
	flag.Parse()
	rand.Seed(time.Now().UnixNano())
}

func dlSong(url string) io.Reader {
	// Setup youtube-dl
	ytdlName := "youtube-dl"
	ytdl := exec.Command(ytdlName, url, "-o", "-")
	r, _ := ytdl.StdoutPipe()
	ytdl.Start()
	return r
}

type DiscordData struct {
	session   *discordgo.Session
	guildID   string
	channelID string
}
type MusicPlayer struct {
	queue    []string
	channels struct {
		add2queue  chan string
		setState   chan bool
		getState   chan bool
		setPause   chan bool
		getQueue   chan []string
		dcDataChan chan DiscordData
		quit       chan bool
	}
	dcData DiscordData
}

var mp *MusicPlayer

func newMusicPlayer() *MusicPlayer {
	m := new(MusicPlayer)
	m.channels.add2queue = make(chan string)
	m.channels.setState = make(chan bool)
	m.channels.getState = make(chan bool)
	m.channels.getQueue = make(chan []string)
	m.channels.setPause = make(chan bool)
	m.channels.dcDataChan = make(chan DiscordData)
	m.channels.quit = make(chan bool)
	go m.run()
	return m
}

func (m *MusicPlayer) encodeNext() (*dca.EncodeSession, error) {
	if len(m.queue) == 0 {
		return &dca.EncodeSession{}, nil
	}
	url := m.queue[0]
	m.queue = m.queue[1:]
	r := dlSong(url)
	encSes, err := dca.EncodeMem(r, dca.StdEncodeOptions)
	return encSes, err
}

func (m *MusicPlayer) nextSong(vc *discordgo.VoiceConnection, done chan error) *dca.StreamingSession {
	encSes, err := m.encodeNext()
	if err != nil {
		fmt.Println(err)
	}
	// TODO: fix leak
	// defer encSes.Cleanup()
	streamSes := dca.NewStream(encSes, vc, done)
	return streamSes
}

func (m *MusicPlayer) push(url string) {
	m.channels.add2queue <- url
	m.channels.setState <- true
}

func (m *MusicPlayer) setPause(state bool) {
	m.channels.setPause <- state
}

func (m *MusicPlayer) skip() {
	m.channels.setState <- false
	m.channels.setState <- true
}

func (m *MusicPlayer) getQueue() []string {
	queue := []string{}
	m.channels.getQueue <- queue
	queue = <-m.channels.getQueue
	return queue
}

func (m *MusicPlayer) connect(s *discordgo.Session, guildID, channelID string) {
	data := DiscordData{
		session:   s,
		guildID:   guildID,
		channelID: channelID,
	}
	m.channels.dcDataChan <- data
}

func (m *MusicPlayer) run() (err error) {
	if err != nil {
		fmt.Println(err)
	}

	var guildID, channelID string
	var vc *discordgo.VoiceConnection

	oldIsPlaying := true
	isPlaying := false

	var streamSes *dca.StreamingSession
	done := make(chan error)
	for {
		select {
		case item := <-m.channels.add2queue:
			m.queue = append(m.queue, item)
		case setState := <-m.channels.setState:
			oldIsPlaying = isPlaying
			isPlaying = setState
			if len(m.queue) == 0 && isPlaying {
				break
			}
			if oldIsPlaying == isPlaying {
				continue
			}
			if isPlaying {
				streamSes = m.nextSong(vc, done)
			} else {
				streamSes.SetPaused(true)
			}
		case pstate := <-m.channels.setPause:
			if pstate {
				streamSes.SetPaused(true)
			} else {
				streamSes.SetPaused(false)
			}
		case <-m.channels.getState:
			m.channels.getState <- isPlaying
		case <-m.channels.getQueue:
			m.channels.getQueue <- m.queue
		case dcData := <-m.channels.dcDataChan:
			m.dcData = dcData
			guildID = m.dcData.guildID
			channelID = m.dcData.channelID
			vc, err = m.dcData.session.ChannelVoiceJoin(guildID, channelID, false, true)
			vc.Speaking(true)
		case <-done:
			if len(m.queue) == 0 {
				isPlaying = false
				vc.Speaking(false)
				time.Sleep(250 * time.Millisecond)
				vc.Disconnect()
				break
			} else {
				streamSes = m.nextSong(vc, done)
			}
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
			mp.connect(s, g.ID, vs.ChannelID)
			i++
			mp.push(args[i])
			s.ChannelMessageSend(m.ChannelID, "Track added to queue")
		case "--list", "-l":
			queue := mp.getQueue()
			msg := "Current queue:\n" + strings.Join(queue, "\n")
			s.ChannelMessageSend(m.ChannelID, msg)
		case "--skip", "-s":
			s.ChannelMessageSend(m.ChannelID, "Skipping current track...")
			mp.skip()
		case "--pause", "-z":
			s.ChannelMessageSend(m.ChannelID, "Pausing playback")
			mp.setPause(true)
		case "--resume", "-r":
			s.ChannelMessageSend(m.ChannelID, "Resuming playback")
			mp.setPause(false)
		case "--restart":
			// Yeah I know this is pretty stupid...
			main()
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

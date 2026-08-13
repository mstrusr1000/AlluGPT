package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type groqRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type groqResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`

	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func main() {
	loadEnv(".env")

	token := os.Getenv("DISCORD_TOKEN")
	if token == "" {
		log.Fatal("Missing DISCORD_TOKEN in .env")
	}

	if os.Getenv("GROQ_API_KEY") == "" {
		log.Fatal("Missing GROQ_API_KEY in .env")
	}

	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Fatal(err)
	}

	dg.AddHandler(onMessage)

	dg.Identify.Intents = discordgo.IntentsGuildMessages |
		discordgo.IntentsDirectMessages |
		discordgo.IntentsMessageContent

	if err := dg.Open(); err != nil {
		log.Fatal(err)
	}
	defer dg.Close()

	log.Println("Discord Groq bot is running. Mention it or DM it.")
	select {}
}

func onMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.Bot {
		return
	}

	botID := s.State.User.ID
	mention1 := "<@" + botID + ">"
	mention2 := "<@!" + botID + ">"

	isDM := m.GuildID == ""
	mentioned := strings.Contains(m.Content, mention1) || strings.Contains(m.Content, mention2)

	if !isDM && !mentioned {
		return
	}

	userText := strings.TrimSpace(m.Content)
	userText = strings.ReplaceAll(userText, mention1, "")
	userText = strings.ReplaceAll(userText, mention2, "")
	userText = strings.TrimSpace(userText)

	if userText == "" {
		userText = "Hello"
	}

	reply, err := askGroq(userText)
	if err != nil {
		reply = "Error: " + err.Error()
	}

	sendReply(s, m.ChannelID, reply)
}

func askGroq(userText string) (string, error) {
	systemPromptBytes, err := os.ReadFile("prompt.txt")
	if err != nil {
		return "", fmt.Errorf("could not read prompt.txt: %w", err)
	}

	model := os.Getenv("GROQ_MODEL")
	if model == "" {
		model = "llama-3.3-70b-versatile"
	}

	reqBody := groqRequest{
		Model: model,
		Messages: []chatMessage{
			{
				Role:    "system",
				Content: strings.TrimSpace(string(systemPromptBytes)),
			},
			{
				Role:    "user",
				Content: userText,
			},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(
		http.MethodPost,
		"https://api.groq.com/openai/v1/chat/completions",
		bytes.NewReader(jsonBody),
	)
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+os.Getenv("GROQ_API_KEY"))

	client := &http.Client{
		Timeout: 60 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var parsed groqResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("bad Groq response: %s", string(raw))
	}

	if resp.StatusCode != http.StatusOK {
		if parsed.Error != nil && parsed.Error.Message != "" {
			return "", fmt.Errorf("Groq status %d: %s", resp.StatusCode, parsed.Error.Message)
		}
		return "", fmt.Errorf("Groq status %d: %s", resp.StatusCode, string(raw))
	}

	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("no response from Groq")
	}

	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

func sendReply(s *discordgo.Session, channelID, text string) {
	if strings.TrimSpace(text) == "" {
		text = "(empty response)"
	}

	const limit = 2000
	runes := []rune(text)

	for len(runes) > limit {
		chunk := string(runes[:limit])
		runes = runes[limit:]

		if _, err := s.ChannelMessageSend(channelID, chunk); err != nil {
			log.Println("send error:", err)
			return
		}
	}

	if _, err := s.ChannelMessageSend(channelID, string(runes)); err != nil {
		log.Println("send error:", err)
	}
}

func loadEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	lines := strings.Split(string(data), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		value = strings.Trim(value, `"'`)

		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
}

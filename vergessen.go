package main

// Vergessen
//

import (
	"bufio"
	"errors"
	"fmt"
	"github.com/imipolexg/vergessen/deck"
	"io"
	"io/ioutil"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

var maxStudy = 20

var dbVersion = 1
var defaultHardness = 2

// This is a hack to work around the initialization loop caused by showHelp's
// reference to cmds
func init() {
	cmds["?"] = Command{showHelp, "show this help"}
}

type Command struct {
	Callback func(*deck.Deck, []string) error
	Help     string
}

var cmds map[string]Command = map[string]Command{
	"del":   {delCard, "delete a card by id"},
	"due":   {dueCards, "see the cards due"},
	"edit":  {editCard, "edit a card"},
	"list":  {list, "list all cards in the deck."},
	"new":   {newCard, "create a new card"},
	"quit":  {quit, "quit"},
	"show":  {showCard, "show a card's prompt and answer"},
	"study": {study, "study all due cards."},
}

var quitError error = errors.New("Peace!")

func main() {
	if len(os.Args[1:]) < 1 {
		fmt.Fprintln(os.Stderr, "No deck specified")
		os.Exit(1)
	}

	d, err := deck.OpenDeck(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
	}
	defer d.Close()

	fmt.Println("Opened deck", os.Args[1])
	fmt.Println(len(d.Cards), "cards. Enter ? for help.")

	rdr := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("nichtvergessen> ")
		input, err := rdr.ReadString('\n')
		if err != nil {
			return
		}

		input = strings.Trim(input, " \n\t\r")
		if input == "" {
			continue
		}

		tokens := strings.Split(input, " ")
		cmd, ok := cmds[tokens[0]]
		if !ok {
			fmt.Println("Don't know what to do with '", tokens[0], "'. Enter '?' for help")
			continue
		}

		err = cmd.Callback(d, tokens[1:])
		if err != nil && err != quitError {
			fmt.Println("Bad news bears:", err)
		}

		if err == quitError {
			fmt.Println("Ill will rest in peace yo' I'm out")
			return
		}
	}
}

func showHelp(d *deck.Deck, args []string) error {
	fmt.Println("The following commands are available\n")

	cmdnames := make([]string, len(cmds))
	i := 0
	for name := range cmds {
		cmdnames[i] = name
		i++
	}
	sort.Sort(sort.StringSlice(cmdnames))

	tabwrt := new(tabwriter.Writer)
	tabwrt.Init(os.Stdout, 0, 8, 0, '\t', 0)
	for _, name := range cmdnames {
		fmt.Fprintf(tabwrt, " %s\t%s\n", name, cmds[name].Help)
	}
	tabwrt.Flush()
	fmt.Println()

	return nil
}

func dueCards(d *deck.Deck, args []string) error {
	due := 0
	now := time.Now().Unix()
	for _, card := range d.Cards {
		if card.NextRep.Unix() < now {
			due++
		}
	}

	fmt.Println(due, "cards due.")
	return nil
}

func study(d *deck.Deck, args []string) error {
	var studied = 0
	for _, card := range d.Cards {
		now := time.Now()

		if now.Unix() < card.NextRep.Unix() {
			continue
		}

		fmt.Print("\n")
		fmt.Println(card.Prompt)
		_, err := getInput("Press ENTER to see the ANSWER")
		if err != nil {
			return err
		}

		var hardness int
		for {
			fmt.Println(card.Answer)
			hardStr, err := getInput("Enter HARDNESS (1-5) and hit ENTER> ")
			if err != nil {
				return err
			}

			// If the user just hits enter, make hardness == 2
			if hardStr == "\n" {
				hardness = defaultHardness
				break
			} else {
				hardness, err = strconv.Atoi(strings.Trim(hardStr, " \n\t\r"))
				if err == nil {
					break
				}

				fmt.Printf("Error reading hardness: %v\n", err)
			}
		}

		card.CalcNextRep(hardness)
		d.Dirty = true

		studied++
		if studied > maxStudy {
			break
		}
	}

	err := d.Sync()
	return err
}

func quit(d *deck.Deck, args []string) error {
	return quitError
}

func list(d *deck.Deck, args []string) error {
	tabwrt := new(tabwriter.Writer)
	tabwrt.Init(os.Stdout, 0, 8, 1, '\t', 0)

	fmt.Fprintln(tabwrt, "Id\tReps\tDue\tPrompt")

	div := strings.Repeat("-----\t", 4)
	div = div[:len(div)-1]
	fmt.Fprintln(tabwrt, div)

	for _, card := range d.Cards {
		fmt.Fprintf(tabwrt, "%d\t%d", card.Id, card.Reps)

		due := fmtDue(card.NextRep.Unix())
		fmt.Fprintf(tabwrt, "\t%s", due)

		var prompt string
		displayLen := 42
		if len(card.Prompt) < displayLen {
			prompt = card.Prompt
		} else {
			prompt = card.Prompt[:displayLen] + "..."
		}

		re, err := regexp.Compile(`\s+`)
		if err != nil {
			return err
		}
		prompt = re.ReplaceAllString(prompt, " ")
		fmt.Fprintf(tabwrt, "\t%s\n", prompt)
	}
	tabwrt.Flush()

	return nil
}

func fmtDue(dueTime int64) string {
	now := time.Now().Unix()
	until := dueTime - now

	if until < 0 {
		return "now"
	}

	days := int(until / (24 * 60 * 60))
	durationInt := 0
	durationNoun := "day"
	plural := false

	switch {
	case days == 0:
		hours := int(until / (60 * 60))
		if hours > 0 {
			plural = true
		}
		durationInt = hours
		durationNoun = "hour"
	case days > 0 && days < 7:
		if days > 1 {
			plural = true
		}
		durationInt = days
	case days >= 7 && days <= 365:
		weeks := days / 7
		if weeks > 1 {
			plural = true
		}
		durationNoun = "week"
		durationInt = weeks
	default:
		months := days / 30
		if months > 1 {
			plural = true
		}
		durationNoun = "month"
		durationInt = months
	}

	if plural {
		durationNoun += "s"
	}

	return fmt.Sprintf("%d %s", durationInt, durationNoun)
}

func cardNumberFromArgs(args []string) (int, error) {
	if len(args) < 1 {
		return 0, errors.New("No card # given")
	}

	num, err := strconv.Atoi(args[0])
	if err != nil {
		return 0, err
	}

	return num, nil
}

func showCard(d *deck.Deck, args []string) error {
	id, err := cardNumberFromArgs(args)
	if err != nil {
		return err
	}

	if id > len(d.Cards)-1 || id < 0 {
		return errors.New("Invalid card id")
	}

	c := d.Cards[id]
	fmt.Println("PROMPT\n")
	fmt.Println(c.Prompt)
	fmt.Println("ANSWER\n")
	fmt.Println(c.Answer)

	return nil
}

func delCard(d *deck.Deck, args []string) error {
	id, err := cardNumberFromArgs(args)
	if err != nil {
		return err
	}
	d.DeleteCard(id)

	return nil
}

func editCard(d *deck.Deck, args []string) error {
	id, err := cardNumberFromArgs(args)
	if err != nil {
		return err
	}

	var c *deck.Card = nil
	for _, card := range d.Cards {
		if card.Id == id {
			c = card
			break
		}
	}
	if c == nil {
		return errors.New(fmt.Sprintf("Unknown card id = %d", id))
	}

	_, err = getInput("Press ENTER to edit the PROMPT, or CTRL+D to leave as is")
	if err == nil {
		newPrompt, err := spawnEditor("prompt", c.Prompt)
		if err != nil {
			return err
		}
		c.Prompt = newPrompt
	} else if err != nil && err != io.EOF {
		return err
	} else {
		fmt.Print("\n")
	}

	_, err = getInput("Press ENTER to edit the ANSWER, or CTRL+D to leave as is")
	if err == nil {
		newAnswer, err := spawnEditor("answer", c.Answer)
		if err != nil {
			return err
		}
		c.Answer = newAnswer
	} else if err != nil && err != io.EOF {
		return err
	} else {
		fmt.Print("\n")
	}

	return nil
}

func spawnEditor(prefix, contents string) (string, error) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		return "", errors.New("Set your EDITOR env variable!")
	}

	tmpFile, err := ioutil.TempFile("/tmp", fmt.Sprintf("vergessen.%s.", prefix))
	if err != nil {
		return "", err
	}

	if len(contents) > 0 {
		if _, err := tmpFile.WriteString(contents); err != nil {
			return "", err
		}
	}
	tmpFile.Close()

	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	cmdArgs := []string{"/bin/sh", "-c", fmt.Sprintf("%s %s", editor, tmpPath)}
	fmt.Println(cmdArgs)
	procAttr := os.ProcAttr{
		"",
		nil,
		[]*os.File{
			os.Stdin,
			os.Stdout,
			os.Stderr,
		},
		nil,
	}

	proc, err := os.StartProcess("/bin/sh", cmdArgs, &procAttr)
	if err != nil {
		return "", err
	}
	_, err = proc.Wait()
	if err != nil {
		return "", err
	}

	result, err := ioutil.ReadFile(tmpPath)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

func getInput(prompt string) (string, error) {
	rdr := bufio.NewReader(os.Stdin)
	fmt.Print(prompt)
	input, err := rdr.ReadString('\n')
	if err != nil {
		return "", err
	}
	return input, nil
}

func newCard(d *deck.Deck, args []string) error {
	_, err := getInput("Press ENTER to edit the card PROMPT")
	if err != nil {
		return err
	}

	prompt, err := spawnEditor("prompt", "Write the PROMPT here and save+quit")
	if err != nil {
		return err
	}
	fmt.Println("Prompt:", prompt)

	_, err = getInput("Press ENTER to edit the card ANSWER")
	if err != nil {
		return err
	}
	answer, err := spawnEditor("answer", "Write the ANSWER here and save+quit")
	if err != nil {
		return err
	}

	fmt.Println("Answer:", answer)

	card := deck.NewCard(prompt, answer)
	d.AddCard(card)

	return nil
}

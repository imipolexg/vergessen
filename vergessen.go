package main

// Vergessen
//
// A simple implementation of the SM-2 algorithm, developed by P.A. Wozniak
//
// See https://www.supermemo.com/english/ol/sm2.htm
//
// Cards are pairs of prompts and answers.
// The prompt is displayed, and the user has to come up with the answer. Then
// the program shows the answer. The user then indicates how difficult it was
// for them to come up with the answer based on the prompt. The selection of a
// difficulty level determines the amount of time the program should wait before
// showing the prompt again.
//
// The interval determination is fairly simple. It is determined by a function,
// I(n) that calculates the number of days to delay a card. n is the number of
// times that the user has seen the prompt/answer pair, and I is defined
// as follows:
//
// If n = 1, I(n) = 1
// If n = 2, I(n) = 6
// If n > 2, I(n) = I(n-1) * EF
//
// EF, the most complicated part of SM-2, is the 'easiness factor'.
//
// EF is determined by the following recursive function:
//
// EF = f(EF', q)
//
// Where q is the quality rating the user provides (between 5 and 0), EF'
// is the previous EF, or 2.5 if this is the first time n > 2, and where f is:
//
// EF = f(EF', q) = EF' - 0.8 + 0.28 * q - 0.02 * q * q
//
// So, for n == 3, with q (hardness) of 3, we calculate like so:
//
// I(n = 3) = I(2) * 2.5 - 0.8 + 0.28 * 3 - 0.02 * 3 * 3
//  or
// I(3) = 6 * 2.5 - 0.8 * 3 - 0.02 * 3 * 3 = 14.16
//
// For n = 4, we calculate:
//
// I(4) = I(3) * 2.5 - 0.8 etc., which means we have to expand the calculation
// for all the preceding intervals
//
// NOTE: The default EF has been changed to 1.75 instead of 2.5 and will be
// NOTE: user configurable

import (
	"bufio"
	"errors"
	"fmt"
	"github.com/imipolexg/vergessen/deck"
	_ "github.com/mattn/go-sqlite3"
	"io"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"time"
)

var maxStudy = 20

var cmds map[string]func(*deck.Deck, []string) error = map[string]func(*deck.Deck, []string) error{
	"study": study,
	"list":  list,
	"quit":  quit,
	"new":   newCard,
	"del":   delCard,
	"edit":  editCard,
}

var quitError error = errors.New("Peace!")

func main() {
	if len(os.Args[1:]) < 1 {
		fmt.Fprintln(os.Stderr, "No deck specified")
		os.Exit(1)
	}

	deck, err := deck.OpenDeck(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
	}
	defer deck.Close()

	fmt.Println("Opened deck", os.Args[1])
	fmt.Println(len(deck.Cards), "cards. Enter ? for help.")

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
		cmdFunc, ok := cmds[tokens[0]]
		if !ok {
			fmt.Println("Don't know what to do with '", tokens[0], "'. Enter '?' for help")
			continue
		}

		err = cmdFunc(deck, tokens[1:])
		if err != nil && err != quitError {
			fmt.Println("Bad news bears:", err)
		}

		if err == quitError {
			fmt.Println("Ill will rest in peace yo' I'm out")
			return
		}
	}
}

func study(d *deck.Deck, args []string) error {
	var studied = 0
	for _, card := range d.Cards {
		now := time.Now()

		if now.Unix() < card.NextRep.Unix() {
			continue
		}

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

			hardness, err = strconv.Atoi(strings.Trim(hardStr, " \n\t\r"))
			if err == nil {
				break
			}

			fmt.Printf("Error reading hardness: %v\n", err)
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
	for _, card := range d.Cards {
		fmt.Printf("%d (%v): %s", card.Id, card.NextRep, card.Prompt)
		if card.Prompt[len(card.Prompt)-1] != '\n' {
			fmt.Print("\n")
		}
	}

	return nil
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

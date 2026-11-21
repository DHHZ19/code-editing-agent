package main

// https://ampcode.com/notes/how-to-build-an-agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

func main() {
	client := openai.NewClient()

	scanner := bufio.NewScanner(os.Stdin)

	getUserMessage := func() (string, bool) {
		if !scanner.Scan() {
			return "", false
		}
		return scanner.Text(), true
	}

	tools := []ToolDefinition{ReadFileDefinition, ListFilesDefinition, EditFileDefinition}
	agent := NewAgent(&client, getUserMessage, tools)
	err := agent.Run(context.TODO())
	if err != nil {
		fmt.Printf("Error %s\n", err.Error())
	}
}

func NewAgent(client *openai.Client, getUserMessage func() (string, bool), tools []ToolDefinition) *Agent {
	return &Agent{
		client:         client,
		getUserMessage: getUserMessage,
		tools:          tools,
	}
}

type Agent struct {
	client         *openai.Client
	getUserMessage func() (string, bool)
	tools          []ToolDefinition
}

func (a *Agent) Run(ctx context.Context) error {
	var previousResponseID string

	fmt.Println("Chat with ChatGPT (use 'ctrl+c' to quit)")

	for {
		fmt.Print("\033[94mYou\033[0m: ")

		userInput, ok := a.getUserMessage()

		if !ok {
			break
		}

		input := []responses.ResponseInputItemUnionParam{
			{OfMessage: &responses.EasyInputMessageParam{
				Role:    "user",
				Content: responses.EasyInputMessageContentUnionParam{OfString: openai.String(userInput)},
			}},
		}

		for {
			message, err := a.runInference(ctx, input, previousResponseID)
			if err != nil {
				return err
			}
			previousResponseID = message.ID
			input = nil

			var outputText strings.Builder
			for _, item := range message.Output {
				switch item.Type {
				case "message":
					for _, content := range item.Content {
						if content.Type == "output_text" {
							outputText.WriteString(content.AsOutputText().Text)
						}
					}
				case "function_call":
					result, err := a.executeTool(item.Name, json.RawMessage(item.Arguments.OfString))
					if err != nil {
						result = fmt.Sprintf("Tool error: %v", err)
					}
					input = append(input, responses.ResponseInputItemUnionParam{
						OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
							CallID: openai.String(item.CallID),
							Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
								OfString: openai.String(result),
							},
						},
					})
				}
			}

			if len(input) == 0 {
				fmt.Printf("\033[93mChatGPT\033[0m: %s\n", outputText.String())
				break
			}
		}
	}
	return nil
}

func (a *Agent) executeTool(name string, input json.RawMessage) (string, error) {
	for _, tool := range a.tools {
		if tool.Name == name {
			if tool.Function == nil {
				return "", fmt.Errorf("tool %q has no implementation", name)
			}

			fmt.Printf("\u001b[92mtool\u001b[0m: %s(%s)\n", name, input)
			return tool.Function(input)
		}
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

func (a *Agent) runInference(ctx context.Context, input []responses.ResponseInputItemUnionParam, previousResponseID string) (*responses.Response, error) {
	openAITools := []responses.ToolUnionParam{}
	for _, tool := range a.tools {
		openAITools = append(openAITools, responses.ToolUnionParam{OfFunction: &responses.FunctionToolParam{
			Name:        tool.Name,
			Description: openai.String(tool.Description),
			Parameters:  tool.InputSchema,
		}})
	}

	params := responses.ResponseNewParams{
		Model:           openai.ChatModelGPT6Luna,
		MaxOutputTokens: openai.Int(1024),
		Input:           responses.ResponseNewParamsInputUnion{OfInputItemList: input},
		Tools:           openAITools,
	}
	if previousResponseID != "" {
		params.PreviousResponseID = openai.String(previousResponseID)
	}

	return a.client.Responses.New(ctx, params)
}

type ToolDefinition struct {
	Name        string                    `json:"name"`
	Description string                    `json:"description"`
	InputSchema openai.FunctionParameters `json:"input_schema"`
	Function    func(input json.RawMessage) (string, error)
}

var ReadFileDefinition = ToolDefinition{
	Name:        "read_file",
	Description: "Read the contents of a given relative file path. Use this when you want to see what's inside a file. Do not use this with directory names.",
	InputSchema: ReadFileInputSchema,
	Function:    ReadFile,
}

type ReadFileInput struct {
	Path string `json:"path" jsonschema_description:"The relative path of a file in the working directory."`
}

var ReadFileInputSchema = openai.FunctionParameters{
	"type": "object",
	"properties": map[string]any{
		"path": map[string]any{
			"type":        "string",
			"description": "The relative path of a file in the working directory.",
		},
	},
	"required":             []string{"path"},
	"additionalProperties": false,
}

func ReadFile(input json.RawMessage) (string, error) {
	readFileInput := ReadFileInput{}
	if err := json.Unmarshal(input, &readFileInput); err != nil {
		return "", fmt.Errorf("decode read_file arguments: %w", err)
	}

	content, err := os.ReadFile(readFileInput.Path)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

var ListFilesDefinition = ToolDefinition{
	Name:        "list_files",
	Description: "List files and directories at a given path. If no path is provided, lists files in the current directory.",
	InputSchema: ListFilesInputSchema,
	Function:    ListFiles,
}

type ListFilesInput struct {
	Path string `json:"path,omitempty" jsonschema_description:"Optional relative path to list files from. Defaults to current directory if not provided."`
}

var ListFilesInputSchema = openai.FunctionParameters{
	"type": "object",
	"properties": map[string]any{
		"path": map[string]any{
			"type":        "string",
			"description": "Optional relative path to list files from. Defaults to current directory if not provided.",
		},
	},
	"required":             []string{"path"},
	"additionalProperties": false,
}

func ListFiles(input json.RawMessage) (string, error) {
	listFilesInput := ListFilesInput{}

	if err := json.Unmarshal(input, &listFilesInput); err != nil {
		return "", fmt.Errorf("decode list_files arguments: %w", err)
	}

	base, err := os.Getwd()
	if err != nil {
		return "", err
	}

	var abs string

	if listFilesInput.Path != "." && listFilesInput.Path != "" {
		abs, err = filepath.Rel(base, listFilesInput.Path)
		if err != nil {
			return "", err
		}
	} else {
		abs = "."
	}

	d, err := os.ReadDir(abs)
	if err != nil {
		return "", err
	}

	var s strings.Builder
	for _, f := range d {
		if f.IsDir() {
			s.WriteString(f.Name())
			s.WriteString("/ ")
		} else {
			s.WriteString(f.Name())
		}

	}

	return s.String(), err
}

type EditFileInput struct {
	Path    string `json:"path,omitempty" jsonschema_description:"Relative path to edit file from."`
	Replace string `json:"replace,omitempty" jsonschema_description:"The string to replace"`
	String  string `json:"string,omitempty" jsonschema_description:"The string to write"`
}

var EditFileInputSchema = openai.FunctionParameters{
	"type": "object",
	"properties": map[string]any{
		"path": map[string]any{
			"type":        "string",
			"description": "A relative path to the file",
		},
		"replace": map[string]any{
			"type":        "string",
			"description": "the string to replace",
		},
		"string": map[string]any{
			"type":        "string",
			"description": "the string to write",
		},
	},
}

var EditFileDefinition = ToolDefinition{
	Name:        "edit_files",
	Description: "Edit files, edit files by replacing or adding text at the provided path",
	InputSchema: EditFileInputSchema,
	Function:    EditFile,
}

func EditFile(input json.RawMessage) (string, error) {
	editFileInput := &EditFileInput{}
	json.Unmarshal(input, editFileInput)

	content, err := os.ReadFile(editFileInput.Path)
	if err != nil {
		if os.IsNotExist(err) && editFileInput.Replace != "" {
			return "file does not exist", nil
		}
		return "", err
	}

	c := string(content)
	c = strings.ReplaceAll(c, editFileInput.Replace, editFileInput.String)

	err = os.WriteFile(editFileInput.Path, []byte(c), 0666)
	if err != nil {
		return "", err
	}

	return "", nil
}

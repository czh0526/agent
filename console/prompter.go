package console

type UserPrompter interface {
	PromptInput(prompt string) (string, error)
	PromptPassword(prompt string) (string, error)
	PromptConfirm(prompt string) (bool, error)
}

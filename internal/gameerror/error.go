package gameerror

import "fmt"

const (
	CodeOK             = 200
	CodeRoomNotFound   = 404
	CodeRoomFull       = 403
	CodeInternalError  = 500
	CodeInvalidRequest = 400
	CodeAlreadyInRoom  = 409
)

type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("code=%d msg=%s", e.Code, e.Message)
}

func New(code int, msg string) *Error {
	return &Error{Code: code, Message: msg}
}

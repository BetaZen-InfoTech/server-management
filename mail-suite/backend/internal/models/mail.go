package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Folder struct {
	Name      string `json:"name"`
	Delimiter string `json:"delimiter"`
	Total     int    `json:"total"`
	Unread    int    `json:"unread"`
	Special   string `json:"special,omitempty"`
}

type Address struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`
}

type MessageHeader struct {
	UID        uint32    `json:"uid"`
	Folder     string    `json:"folder"`
	MessageID  string    `json:"message_id,omitempty"`
	ThreadKey  string    `json:"thread_key,omitempty"`
	Subject    string    `json:"subject"`
	From       []Address `json:"from"`
	To         []Address `json:"to,omitempty"`
	Cc         []Address `json:"cc,omitempty"`
	Date       time.Time `json:"date"`
	Snippet    string    `json:"snippet"`
	Unread     bool      `json:"unread"`
	Starred    bool      `json:"starred"`
	HasAttach  bool      `json:"has_attach"`
	Size       uint32    `json:"size"`
}

type Thread struct {
	ThreadKey string          `json:"thread_key"`
	Subject   string          `json:"subject"`
	From      []Address       `json:"from"`
	UpdatedAt time.Time       `json:"updated_at"`
	Count     int             `json:"count"`
	Unread    int             `json:"unread"`
	Snippet   string          `json:"snippet"`
	Messages  []MessageHeader `json:"messages,omitempty"`
}

type MessageBody struct {
	UID        uint32      `json:"uid"`
	MessageID  string      `json:"message_id"`
	Subject    string      `json:"subject"`
	From       []Address   `json:"from"`
	To         []Address   `json:"to,omitempty"`
	Cc         []Address   `json:"cc,omitempty"`
	Bcc        []Address   `json:"bcc,omitempty"`
	ReplyTo    []Address   `json:"reply_to,omitempty"`
	Date       time.Time   `json:"date"`
	HTML       string      `json:"html,omitempty"`
	Text       string      `json:"text,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

type Attachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size"`
	IsInline    bool   `json:"is_inline,omitempty"`
}

type SendRequest struct {
	To          []Address `json:"to" validate:"required,min=1"`
	Cc          []Address `json:"cc,omitempty"`
	Bcc         []Address `json:"bcc,omitempty"`
	Subject     string    `json:"subject"`
	HTML        string    `json:"html,omitempty"`
	Text        string    `json:"text,omitempty"`
	SignatureID string    `json:"signature_id,omitempty"`
	InReplyTo   string    `json:"in_reply_to,omitempty"`
	References  []string  `json:"references,omitempty"`
}

type MessageFlagRequest struct {
	Unread  *bool `json:"unread,omitempty"`
	Starred *bool `json:"starred,omitempty"`
	Folder  string `json:"folder,omitempty"` // move to folder (archive/spam/trash/custom)
}

// Mongo-side indexes for fast paging and sync state per (account, folder).
type ThreadIndex struct {
	ID         primitive.ObjectID `bson:"_id,omitempty"`
	AccountID  primitive.ObjectID `bson:"account_id"`
	Folder     string             `bson:"folder"`
	ThreadKey  string             `bson:"thread_key"`
	Subject    string             `bson:"subject"`
	FromName   string             `bson:"from_name"`
	FromAddr   string             `bson:"from_addr"`
	Snippet    string             `bson:"snippet"`
	UpdatedAt  time.Time          `bson:"updated_at"`
	Count      int                `bson:"count"`
	Unread     int                `bson:"unread"`
}

type MessageIndex struct {
	ID         primitive.ObjectID `bson:"_id,omitempty"`
	AccountID  primitive.ObjectID `bson:"account_id"`
	Folder     string             `bson:"folder"`
	UID        uint32             `bson:"uid"`
	MessageID  string             `bson:"message_id,omitempty"`
	ThreadID   primitive.ObjectID `bson:"thread_id,omitempty"`
	Subject    string             `bson:"subject"`
	Date       time.Time          `bson:"date"`
}

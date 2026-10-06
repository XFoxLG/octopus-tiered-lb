package model

import (
	"encoding/json"
	"strconv"
)

// Keep numeric IDs for old API consumers, and supply exact decimal identities
// for JavaScript clients. Database keys and backup decoding remain unchanged.
func (v RelayLog) MarshalJSON() ([]byte, error) {
	type plain RelayLog
	return json.Marshal(struct {
		plain
		IDString string `json:"id_str"`
	}{plain(v), strconv.FormatInt(v.ID, 10)})
}

func (v RelayLogListItem) MarshalJSON() ([]byte, error) {
	type plain RelayLogListItem
	return json.Marshal(struct {
		plain
		IDString string `json:"id_str"`
	}{plain(v), strconv.FormatInt(v.ID, 10)})
}

func (v ErrorLog) MarshalJSON() ([]byte, error) {
	type plain ErrorLog
	return json.Marshal(struct {
		plain
		IDString string `json:"id_str"`
	}{plain(v), strconv.FormatInt(v.ID, 10)})
}

func (v RelayLogContentRef) MarshalJSON() ([]byte, error) {
	type plain RelayLogContentRef
	return json.Marshal(struct {
		plain
		IDString         string `json:"id_str"`
		RelayLogIDString string `json:"relay_log_id_str"`
	}{plain(v), strconv.FormatInt(v.ID, 10), strconv.FormatInt(v.RelayLogID, 10)})
}

// Do not inherit the embedded reference's marshaler: it would omit Text.
func (v RelayLogCapturedContent) MarshalJSON() ([]byte, error) {
	type plain RelayLogContentRef
	return json.Marshal(struct {
		plain
		IDString         string `json:"id_str"`
		RelayLogIDString string `json:"relay_log_id_str"`
		Text             string `json:"text,omitempty"`
	}{plain(v.RelayLogContentRef), strconv.FormatInt(v.ID, 10), strconv.FormatInt(v.RelayLogID, 10), v.Text})
}

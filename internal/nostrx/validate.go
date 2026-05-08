package nostrx

import "errors"

func ValidateSignedEvent(event Event) error {
	if event.CreatedAt <= 0 {
		return errors.New("event created_at is required")
	}
	externalEvent, err := toExternalEvent(event)
	if err != nil {
		return err
	}
	if !externalEvent.CheckID() {
		return errors.New("event id does not match payload")
	}
	if !externalEvent.VerifySignature() {
		return errors.New("event signature verification failed")
	}
	return nil
}

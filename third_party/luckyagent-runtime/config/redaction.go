package config

import (
	"reflect"
	"strings"
)

// RedactSecrets returns a copy suitable for local configuration UIs and API
// responses. It deliberately keeps the fields present so clients can replace a
// secret without ever receiving the old value.
func RedactSecrets(cfg *Config) *Config {
	redacted := cloneConfig(cfg)
	if redacted == nil {
		return nil
	}
	redactConfigValue(reflect.ValueOf(redacted).Elem(), false)
	return redacted
}

// PreserveRedactedSecrets keeps existing values where a configuration form sent
// an empty redacted secret. Supplying a non-empty value explicitly replaces it.
func PreserveRedactedSecrets(current, submitted *Config) {
	if current == nil || submitted == nil {
		return
	}
	preserveConfigValue(reflect.ValueOf(current).Elem(), reflect.ValueOf(submitted).Elem(), false)
}

func redactConfigValue(value reflect.Value, inheritedSensitive bool) {
	if !value.IsValid() {
		return
	}
	switch value.Kind() {
	case reflect.Struct:
		typeOfValue := value.Type()
		for index := 0; index < value.NumField(); index++ {
			field := value.Field(index)
			if !field.CanSet() {
				continue
			}
			name := jsonFieldName(typeOfValue.Field(index).Tag.Get("json"), typeOfValue.Field(index).Name)
			redactConfigValue(field, inheritedSensitive || secretFieldName(name))
		}
	case reflect.String:
		if inheritedSensitive && value.CanSet() {
			value.SetString("")
		}
	case reflect.Map:
		if inheritedSensitive && value.CanSet() {
			value.Set(reflect.Zero(value.Type()))
			return
		}
		for _, key := range value.MapKeys() {
			entry := value.MapIndex(key)
			copyEntry := reflect.New(entry.Type()).Elem()
			copyEntry.Set(entry)
			redactConfigValue(copyEntry, false)
			value.SetMapIndex(key, copyEntry)
		}
	case reflect.Slice:
		if inheritedSensitive && value.CanSet() {
			value.Set(reflect.Zero(value.Type()))
			return
		}
		for index := 0; index < value.Len(); index++ {
			redactConfigValue(value.Index(index), false)
		}
	}
}

func preserveConfigValue(current, submitted reflect.Value, inheritedSensitive bool) {
	if !current.IsValid() || !submitted.IsValid() || current.Type() != submitted.Type() {
		return
	}
	switch submitted.Kind() {
	case reflect.Struct:
		typeOfValue := submitted.Type()
		for index := 0; index < submitted.NumField(); index++ {
			field := submitted.Field(index)
			if !field.CanSet() {
				continue
			}
			name := jsonFieldName(typeOfValue.Field(index).Tag.Get("json"), typeOfValue.Field(index).Name)
			preserveConfigValue(current.Field(index), field, inheritedSensitive || secretFieldName(name))
		}
	case reflect.String:
		if inheritedSensitive && submitted.CanSet() && submitted.String() == "" && current.String() != "" {
			submitted.SetString(current.String())
		}
	case reflect.Map:
		if inheritedSensitive && submitted.CanSet() && submitted.Len() == 0 && current.Len() > 0 {
			submitted.Set(current)
			return
		}
		for _, key := range submitted.MapKeys() {
			currentEntry := current.MapIndex(key)
			if !currentEntry.IsValid() {
				continue
			}
			submittedEntry := submitted.MapIndex(key)
			copyEntry := reflect.New(submittedEntry.Type()).Elem()
			copyEntry.Set(submittedEntry)
			preserveConfigValue(currentEntry, copyEntry, false)
			submitted.SetMapIndex(key, copyEntry)
		}
	case reflect.Slice:
		if inheritedSensitive && submitted.CanSet() && submitted.Len() == 0 && current.Len() > 0 {
			submitted.Set(current)
			return
		}
		for index := 0; index < submitted.Len() && index < current.Len(); index++ {
			preserveConfigValue(current.Index(index), submitted.Index(index), false)
		}
	}
}

func jsonFieldName(tag, fallback string) string {
	if tag == "" {
		return strings.ToLower(fallback)
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" || name == "-" {
		return strings.ToLower(fallback)
	}
	return name
}

func secretFieldName(name string) bool {
	name = strings.ToLower(name)
	return strings.Contains(name, "api_key") || strings.Contains(name, "token") || strings.Contains(name, "secret") || strings.Contains(name, "password") || strings.Contains(name, "extra_headers")
}

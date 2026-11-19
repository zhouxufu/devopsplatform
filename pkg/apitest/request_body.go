package apitest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"strings"
)

const MaxRequestBody = 256 << 10

// Files are explicit, encrypted request inputs; never server filesystem paths.
type FileContent struct {
	Pending     bool   `json:"pending,omitempty"`
	Name        string `json:"name"`
	ContentType string `json:"content_type,omitempty"`
	Data        string `json:"data_base64"`
}
type FormField struct {
	Name  string       `json:"name"`
	Value string       `json:"value"`
	File  *FileContent `json:"file,omitempty"`
}

func sanitizedFile(f *FileContent) *FileContent {
	if f == nil {
		return nil
	}
	return &FileContent{Name: f.Name, ContentType: f.ContentType, Data: "[REDACTED]"}
}
func fileBytes(f *FileContent) ([]byte, error) {
	if f == nil || f.Pending || f.Name == "" || len(f.Name) > 200 || strings.ContainsAny(f.Name, "/\\\r\n\x00") {
		return nil, fmt.Errorf("请选择文件，文件名不能包含目录")
	}
	if len(f.Data) > base64.StdEncoding.EncodedLen(MaxRequestBody) {
		return nil, fmt.Errorf("文件超过 256 KiB")
	}
	raw, err := base64.StdEncoding.DecodeString(f.Data)
	if err != nil || len(raw) > MaxRequestBody {
		return nil, fmt.Errorf("文件内容无效或超过 256 KiB")
	}
	if f.ContentType != "" {
		if _, _, err := mime.ParseMediaType(f.ContentType); err != nil || strings.ContainsAny(f.ContentType, "\r\n") {
			return nil, fmt.Errorf("文件类型无效")
		}
	}
	return raw, nil
}
func validateBody(r Request) error {
	if len(r.Form) > 30 {
		return fmt.Errorf("最多 30 个表单字段")
	}
	switch r.BodyType {
	case "", "text":
	case "json":
		if r.Body != "" && !json.Valid([]byte(r.Body)) {
			return fmt.Errorf("JSON 正文格式无效")
		}
	case "urlencoded", "multipart":
	case "binary":
		if _, err := fileBytes(r.File); err != nil {
			return err
		}
	default:
		return fmt.Errorf("请求正文类型无效")
	}
	if r.BodyType != "multipart" && r.BodyType != "urlencoded" && len(r.Form) > 0 {
		return fmt.Errorf("当前正文类型不接受表单字段")
	}
	if r.BodyType != "binary" && r.File != nil {
		return fmt.Errorf("当前正文类型不接受二进制文件")
	}
	if (r.BodyType == "binary" || r.BodyType == "multipart" || r.BodyType == "urlencoded") && r.Body != "" {
		return fmt.Errorf("请清空未使用的文本正文")
	}
	total := 0
	for _, field := range r.Form {
		if strings.TrimSpace(field.Name) == "" || len(field.Name) > 200 || strings.ContainsAny(field.Name, "\r\n\x00") || strings.Contains(field.Name, "{{") {
			return fmt.Errorf("请填写有效表单字段名")
		}
		total += len(field.Value) + len(field.Name)
		if field.File != nil {
			if r.BodyType != "multipart" || field.Value != "" {
				return fmt.Errorf("文件仅支持 multipart 文件字段")
			}
			raw, err := fileBytes(field.File)
			if err != nil {
				return err
			}
			total += len(raw)
		}
	}
	if total > MaxRequestBody {
		return fmt.Errorf("表单和文件合计超过 256 KiB")
	}
	return nil
}
func requestBody(r Request) (string, string, error) {
	if err := validateBody(r); err != nil {
		return "", "", err
	}
	switch r.BodyType {
	case "urlencoded":
		values := url.Values{}
		for _, f := range r.Form {
			values.Add(f.Name, f.Value)
		}
		raw := values.Encode()
		if len(raw) > MaxRequestBody {
			return "", "", fmt.Errorf("编码后正文超过 256 KiB")
		}
		return raw, "application/x-www-form-urlencoded", nil
	case "multipart":
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		for _, f := range r.Form {
			if f.File == nil {
				if err := writer.WriteField(f.Name, f.Value); err != nil {
					return "", "", err
				}
				continue
			}
			raw, _ := fileBytes(f.File)
			ct := f.File.ContentType
			if ct == "" {
				ct = "application/octet-stream"
			}
			headers := textproto.MIMEHeader{}
			headers.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": f.Name, "filename": f.File.Name}))
			headers.Set("Content-Type", ct)
			part, err := writer.CreatePart(headers)
			if err != nil {
				return "", "", err
			}
			if _, err = part.Write(raw); err != nil {
				return "", "", err
			}
		}
		if err := writer.Close(); err != nil {
			return "", "", err
		}
		return buf.String(), writer.FormDataContentType(), nil
	case "binary":
		raw, err := fileBytes(r.File)
		ct := r.File.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		return string(raw), ct, err
	case "json":
		return r.Body, "application/json", nil
	case "text":
		return r.Body, "text/plain; charset=utf-8", nil
	default:
		return r.Body, "", nil
	}
}
func requestBodySecrets(r Request) []string {
	secrets := bodySecrets(r.Body)
	for _, f := range r.Form {
		if SecretKey(f.Name) && f.Value != "" {
			secrets = append(secrets, f.Value)
		}
		if f.File != nil {
			if raw, err := fileBytes(f.File); err == nil && len(raw) > 0 {
				secrets = append(secrets, string(raw), f.File.Data)
				secrets = append(secrets, bodySecrets(string(raw))...)
			}
		}
	}
	if r.File != nil {
		if raw, err := fileBytes(r.File); err == nil && len(raw) > 0 {
			secrets = append(secrets, string(raw), r.File.Data)
			secrets = append(secrets, bodySecrets(string(raw))...)
		}
	}
	return secrets
}
func requestFields(r Request) []string {
	fields := []string{r.Path, r.Body}
	for _, f := range r.Form {
		fields = append(fields, f.Value)
	}
	return fields
}

// Case variables are declared by suite bindings, never inferred from a previous report.
func ValidateCaseReferences(r Request, available map[string]bool, required bool) error {
	fields := requestFields(r)
	for _, v := range r.Headers {
		fields = append(fields, v)
	}
	for _, a := range r.Assertions {
		fields = append(fields, a.Path, a.Expected, a.Event)
	}
	for _, field := range fields {
		if strings.Contains(scopedVariable.ReplaceAllString(field, ""), "{{case.") {
			return fmt.Errorf("用例变量格式无效，请使用 {{case.order_id}}")
		}
		if required {
			for _, m := range scopedVariable.FindAllStringSubmatch(field, -1) {
				if m[1] == "case" && !available["case."+m[2]] {
					return fmt.Errorf("用例变量 %s 未绑定，请通过配置了变量关联的测试集执行", m[2])
				}
			}
		}
	}
	return nil
}

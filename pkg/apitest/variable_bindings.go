package apitest

import (
	"fmt"
	"net/url"
)

// ValidateVariableBindings checks only configured environment inputs. Step and
// data values remain private execution inputs; this function sends no requests.
func ValidateVariableBindings(r Request, a Auth, vars map[string]string) error {
	fields := []string{r.Path, r.Body}
	for _, v := range r.Headers {
		fields = append(fields, v)
	}
	for _, f := range r.Form {
		fields = append(fields, f.Value)
	}
	for _, check := range r.Assertions {
		fields = append(fields, check.Path, check.Expected, check.Event)
	}
	if a.Mode == "bearer" && vars[a.TokenVariable] == "" {
		return fmt.Errorf("认证变量 %s 未绑定或为空", a.TokenVariable)
	}
	if a.Mode == "login" {
		fields = append(fields, a.Path)
		for _, v := range a.Headers {
			fields = append(fields, v)
		}
		if a.BodyType == "form" {
			values, err := url.ParseQuery(a.Body)
			if err != nil {
				return fmt.Errorf("登录表单格式无效")
			}
			for _, rows := range values {
				fields = append(fields, rows...)
			}
		} else {
			fields = append(fields, a.Body)
		}
	}
	for _, field := range fields {
		if _, err := expand(field, vars); err != nil {
			return err
		}
	}
	return nil
}

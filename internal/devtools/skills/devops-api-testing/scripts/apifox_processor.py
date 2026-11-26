"""Translate one Apifox processor from stdin into native case fragments."""

import json
import re
import sys


NAME = r"[A-Za-z_][A-Za-z0-9_]{0,63}"
GUID = re.compile(
    rf"""pm\.variables\.set\(\s*["']({NAME})["']\s*,\s*pm\.variables\.replaceIn\(\s*["']\{{\{{\$guid\}}\}}["']\s*\)\s*\)\s*;"""
)
TOKEN = re.compile(
    rf"""const\s+token\s*=\s*pm\.response\.json\(\)\.data\?\.({NAME});\s*
    if\s*\(typeof\s+token\s*===\s*["']string["']\s*&&\s*token\.length\)\s*\{{\s*
    pm\.environment\.set\(\s*["']({NAME})["']\s*,\s*["']Bearer[ ]["']\s*\+\s*token\);\s*\}}""",
    re.X,
)
MEMBER = re.compile(
    rf"""const\s+sampleId\s*=\s*['"]([A-Za-z0-9_-]+)['"];\s*
    const\s+records\s*=\s*pm\.response\.json\(\)\.data\.({NAME});\s*
    const\s+present\s*=\s*records\.some\(row\s*=>\s*String\(row\.({NAME})\)\s*===\s*sampleId\);\s*
    pm\.test\(\s*(?:"[^"\\]*"|'[^'\\]*')\s*,\s*\(\)\s*=>\s*pm\.expect\(present\)\.to\.equal\((true|false)\)\);""",
    re.X,
)
NUMERIC_FIELDS = re.compile(
    rf"""const\s+record\s*=\s*pm\.response\.json\(\)\.data;\s*
    if\s*\(\s*!record\s*\|\|\s*!Array\.isArray\(record\.({NAME})\)\s*\|\|\s*String\(record\.\1\)\s*!==\s*['"]([0-9]{{1,15}})['"]\s*\)\s*throw\s+Error\((?:"[^"\\]*"|'[^'\\]*')\);\s*
    for\s*\(const\s+key\s+of\s+\[([^\]]+)\]\)\s*\{{\s*
    const\s+n\s*=\s*record\[key\];\s*if\s*\(n===null\|\|n===undefined\|\|!Number\.isFinite\(Number\(n\)\)\)\s*throw\s+Error\((?:"[^"\\]*"|'[^'\\]*')\s*\+\s*key\);\s*
    pm\.variables\.set\(['"]({NAME})['"]\s*\+\s*key\s*,\s*String\(n\)\);\s*\}}""",
    re.X,
)


def schema_check(path, schema):
    return {"type": "schema", "path": path, "expected": json.dumps(schema, ensure_ascii=False)}


def convert(script):
    text = script.strip()
    if not text or len(script) > 16000:
        raise ValueError("请输入单段不超过 16000 字符的处理器脚本")
    cells = {}
    remaining = text
    while match := GUID.match(remaining):
        name = match[1]
        if name in cells:
            raise ValueError("同名变量重复生成，需要核对变量的使用时机")
        cells[name] = {"type": "uuid", "value": ""}
        remaining = remaining[match.end():].lstrip()
    if cells and not remaining:
        return {"data_columns": cells, "references": {key: "{{data." + key + "}}" for key in cells}}
    if match := TOKEN.fullmatch(text):
        field, name = match.groups()
        path = "/data/" + field
        return {
            "assertions": [schema_check(path, {"type": "string", "minLength": 1})],
            "extractions": [{"source": "json", "path": path, "name": name}],
            "references": {name: "Bearer {{step." + name + "}}"},
            "review": "核对下游需要本次登录签发的令牌。响应缺少非空令牌时流程会停止。",
        }
    if match := MEMBER.fullmatch(text):
        value, array, field, present = match.groups()
        choices = [value]
        if re.fullmatch(r"0|[1-9][0-9]{0,15}", value) and int(value) <= 2**53 - 1:
            choices.append(int(value))
        contains = {"contains": {"type": "object", "required": [field], "properties": {field: {"enum": choices}}}}
        rule = contains if present == "true" else {"not": contains}
        return {"assertions": [schema_check("/data/" + array, {"type": "array", "items": {"type": "object"}, **rule})]}
    if match := NUMERIC_FIELDS.fullmatch(text):
        field, expected, keys, prefix = match.groups()
        names = []
        for token in keys.split(","):
            name = re.fullmatch(rf"""(['"])({NAME})\1""", token.strip())
            if not name or not re.fullmatch(NAME, prefix + name[2]) or name[2] in names:
                raise ValueError("数值提取变量名无效或重复")
            names.append(name[2])
        if len(names) > 10:
            raise ValueError("单步最多提取 10 个变量")
        allowed = [expected]
        if str(int(expected)) == expected:
            allowed.append(int(expected))
        return {
            "assertions": [schema_check("/data/" + field, {
                "type": "array", "minItems": 1, "maxItems": 1, "items": {"enum": allowed},
            })],
            "extractions": [{"name": prefix + name, "source": "json", "path": "/data/" + name,
                             "format": "finite_number_text"} for name in names],
            "references": {prefix + name: "{{step." + prefix + name + "}}" for name in names},
            "review": "核对接口契约为数值或十进制数值文本。原 JS 对布尔值、空文本和嵌套数组的隐式转换不纳入数值契约。",
        }
    raise ValueError("此处理器需要核对原脚本的校验、类型转换及变量用途，保留原脚本并标记待迁移")


if __name__ == "__main__":
    try:
        print(json.dumps(convert(sys.stdin.read(16001)), ensure_ascii=False, indent=2))
    except ValueError as error:
        sys.exit(str(error))

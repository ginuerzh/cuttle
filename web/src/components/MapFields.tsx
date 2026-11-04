import { AutoComplete, Button, Form, Input, Typography } from 'antd';
import { MinusCircleOutlined, PlusOutlined } from '@ant-design/icons';

export interface MapRow {
  key: string;
  value: string;
}

export function mapToRows(map?: Record<string, string>): MapRow[] {
  return Object.entries(map ?? {}).map(([key, value]) => ({ key, value }));
}

/** Turn the row editor back into the config map, rejecting blanks and duplicates. */
export function rowsToMap(rows: MapRow[] | undefined, what = '键'): Record<string, string> {
  const out: Record<string, string> = {};
  for (const row of rows ?? []) {
    const key = (row.key ?? '').trim();
    const value = (row.value ?? '').trim();
    if (!key && !value) continue;
    if (!key) throw new Error(`${what}不能为空`);
    if (key in out) throw new Error(`重复的${what}：${key}`);
    out[key] = value;
  }
  return out;
}

const rowStyle: React.CSSProperties = { display: 'flex', gap: 4, marginTop: 4, alignItems: 'center' };
const grow = (flex: number): React.CSSProperties => ({ flex, minWidth: 0 });

/**
 * Structured editor for `map[string]string`. Stored on the form as an array of
 * rows so partially typed entries survive, and converted on save with
 * `rowsToMap`.
 */
export function MapField({
  name,
  label,
  keyPlaceholder,
  valuePlaceholder,
  hint,
  suggestions,
  valueOptions,
  addLabel = '添加映射',
}: {
  name: string;
  label: string;
  keyPlaceholder: string;
  valuePlaceholder: string;
  hint?: string;
  suggestions?: string[];
  valueOptions?: { value: string; label: string }[];
  addLabel?: string;
}) {
  return (
    <Form.Item label={label} style={{ marginBottom: 12 }}>
      {hint ? (
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {hint}
        </Typography.Text>
      ) : null}
      <Form.List name={name}>
        {(fields, { add, remove }) => (
          <>
            {fields.map((field) => (
              <div key={field.key} style={rowStyle}>
                <Form.Item name={[field.name, 'key']} noStyle>
                  <AutoComplete
                    style={grow(1)}
                    options={(suggestions ?? []).map((v) => ({ value: v }))}
                    placeholder={keyPlaceholder}
                  />
                </Form.Item>
                <Typography.Text type="secondary">→</Typography.Text>
                <Form.Item name={[field.name, 'value']} noStyle>
                  {valueOptions ? (
                    <AutoComplete
                      style={grow(1)}
                      options={valueOptions}
                      placeholder={valuePlaceholder}
                      filterOption={(input, option) =>
                        `${option?.value ?? ''} ${option?.label ?? ''}`
                          .toLowerCase()
                          .includes(input.toLowerCase())
                      }
                    />
                  ) : (
                    <Input style={grow(1)} placeholder={valuePlaceholder} />
                  )}
                </Form.Item>
                <Button
                  type="text"
                  size="small"
                  icon={<MinusCircleOutlined />}
                  onClick={() => remove(field.name)}
                  aria-label="删除映射"
                />
              </div>
            ))}
            <Button
              type="dashed"
              size="small"
              block
              icon={<PlusOutlined />}
              style={{ marginTop: 6 }}
              onClick={() => add({ key: '', value: '' })}
            >
              {addLabel}
            </Button>
          </>
        )}
      </Form.List>
    </Form.Item>
  );
}

/** camelCase → snake_case */
export function toSnakeCase(str: string): string {
  return str.replace(/([a-z])([A-Z])/g, '$1_$2').toLowerCase();
}

/** "grantRead" → "read", "grantFullAccess" → "fullaccess" */
export function grantSuffix(method: string): string {
  return method.replace('grant', '').toLowerCase();
}

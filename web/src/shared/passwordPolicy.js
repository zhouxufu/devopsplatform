export const MIN_PASSWORD_LENGTH=6
export function passwordError(value,{creating=false}={}){
 const length=Array.from(value||'').length
 return (creating||length>0)&&length<MIN_PASSWORD_LENGTH?'密码至少 6 位':''
}

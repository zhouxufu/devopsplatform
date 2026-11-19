// Business rules remain independent of the presentation components.
// A successful check never submits or retries an operation.
export const fieldValue=(model,path)=>String(path).split('.').reduce((value,key)=>value?.[key],model)
export async function validateRules(model,rules){
 const errors={}
 for(const [field,checks] of Object.entries(rules||{}))for(const rule of Array.isArray(checks)?checks:[checks]){
  if(!rule)continue
  const value=fieldValue(model,field),empty=value==null||value===''||Array.isArray(value)&&!value.length
  let message=''
  if(rule.required&&empty)message=rule.message||'请填写此项'
  else if(!empty&&rule.type==='number'&&(typeof value!=='number'||!Number.isFinite(value)))message=rule.message||'请输入有效数字'
  else if(!empty&&rule.type==='array'&&!Array.isArray(value))message=rule.message||'请选择有效条目'
  else if(!empty&&rule.match){const pattern=rule.match instanceof RegExp?rule.match:new RegExp(rule.match);pattern.lastIndex=0;if(!pattern.test(String(value)))message=rule.message||'格式不正确'}
  if(!message&&rule.validator)message=await new Promise((resolve,reject)=>{try{const returned=rule.validator(value,resolve);if(returned?.then)returned.then(result=>resolve(result),reject);else if(rule.validator.length<2)resolve(returned)}catch(error){reject(error)}})||''
  if(message){errors[field]={message:String(message)};break}
 }
 return Object.keys(errors).length?errors:undefined
}
export function scrollToValidationField(root,field){const target=[...(root?.querySelectorAll('[data-validation-field]')||[])].find(el=>el.dataset.validationField===field);target?.scrollIntoView({block:'nearest'});target?.querySelector('input:not([disabled]),textarea:not([disabled]),button:not([disabled])')?.focus();return target}
export function setFieldErrors(root,errors,state){for(const key of Object.keys(state))delete state[key];for(const [field,error] of Object.entries(errors||{}))state[field]={...error,element:root};if(errors)scrollToValidationField(root,Object.keys(errors)[0])}
export async function validateFields(root,model,rules,state){
 const checks={...rules}
 for(const el of root?.querySelectorAll('[data-validation-field][data-validation-rules]')||[]){try{checks[el.dataset.validationField]=JSON.parse(el.dataset.validationRules)}catch{throw Error('字段校验配置无效，请重新打开编辑器')}}
 const errors=await validateRules(model,checks)
 setFieldErrors(root,errors,state)
 return errors
}

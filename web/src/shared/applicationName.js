// Labels are presentation only. Never submit them as a service/resource key.
export const applicationName = app => app?.display_name || app?.name || ''
export const applicationIdentifier = app => app?.identifier || app?.name || ''
export const applicationOption = app => ({value:app.name,label:applicationName(app)===applicationIdentifier(app)?applicationName(app):`${applicationName(app)} · ${applicationIdentifier(app)}`})
export const validApplicationIdentifier = value => /^[a-z][a-z0-9-]{0,62}$/.test(value||'')
export function applicationNameError(value){const text=String(value||'').trim();return !text||Array.from(text).length>100?'应用名称须为 1～100 个字符，支持中文':/[\u0000-\u001f\u007f-\u009f]/.test(text)?'应用名称不能包含换行或控制字符':''}
export function announceApplicationName(app){if(window.parent!==window)window.parent.postMessage({type:'application.presentation.changed',name:app.name},location.origin)}

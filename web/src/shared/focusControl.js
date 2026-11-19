export function focusControl(control,options){
 if(typeof control?.focus==='function'){control.focus(options);return}
 const root=control?.$el||control
 const target=root?.matches?.('input,textarea,select,button,[tabindex]')?root:root?.querySelector?.('input:not([disabled]),textarea:not([disabled]),button:not([disabled]),[tabindex]')
 target?.focus(options)
}

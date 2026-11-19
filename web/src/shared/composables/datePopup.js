import { onBeforeUnmount, onMounted, ref } from 'vue'

// Close the calendar before an enclosing dialog reacts to Escape. Native
// calendar cells may hold focus outside the editor's own input handler.
export function useDatePopup() {
  const picker = ref(null), visible = ref(false)
  function escape(event) {
    if (event.key !== 'Escape' || !visible.value) return
    visible.value = false
    event.preventDefault()
    event.stopImmediatePropagation()
  }
  onMounted(() => document.addEventListener('keydown', escape, true))
  onBeforeUnmount(() => document.removeEventListener('keydown', escape, true))
  return { picker, popupVisible:visible, dateVisible: value => { visible.value = value } }
}

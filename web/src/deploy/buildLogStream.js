// The tail socket sends one complete stored snapshot on every connection,
// followed by emitted log records. Match AppendRunLog's record separators.
export function createBuildLogStream() {
  let snapshotPending = true
  return {
    opened() { snapshotPending = true },
    receive(current, frame) {
      if (snapshotPending) {
        snapshotPending = false
        return frame
      }
      return current + '\n' + frame.replace(/\n+$/, '')
    },
  }
}

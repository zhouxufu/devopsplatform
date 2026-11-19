// Separate entry graphs prevent legacy CSS from being loaded into the new shell.
if(new URLSearchParams(location.search).get('platform-page')==='1')import('./legacyMain.js')
else import('./shadcnMain.js')

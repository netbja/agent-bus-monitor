- **busmon's Backup settings name the destination folder** (backup-dest-folder, the person's ask). The C
  page's Backup group now shows `backup.dir` as "Destination folder", editable: an absolute folder, `~/`
  for the home folder, empty for the default. A folder that exists is saved at once; one that does not
  is created (mode 0700, as `agentbus backup` creates it) only after a yes in the confirmation dialog,
  then saved; no creates and saves nothing; a file is refused. The after_* keys stay read-only.

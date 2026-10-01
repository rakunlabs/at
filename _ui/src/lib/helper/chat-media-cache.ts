/** Per-workbench cache, not shared across accounts or workspace switches. Only
 * successful uploads/downloads survive a failed network attempt. */
export function createChatMediaCache(options: {
  download(id: string): Promise<string>;
  upload(dataUrl: string, name: string): Promise<{ id: string }>;
}) {
  const downloads = new Map<string, Promise<string>>();
  const uploads = new Map<string, Promise<string>>();
  return {
    download(id: string): Promise<string> {
      let pending = downloads.get(id);
      if (!pending) {
        pending = Promise.resolve().then(() => options.download(id)).catch(error => {
          if (downloads.get(id) === pending) downloads.delete(id);
          throw error;
        });
        downloads.set(id, pending);
      }
      return pending;
    },
    upload(dataUrl: string, name: string): Promise<string> {
      let pending = uploads.get(dataUrl);
      if (!pending) {
        pending = Promise.resolve().then(() => options.upload(dataUrl, name)).then(object => {
          downloads.set(object.id, Promise.resolve(dataUrl));
          return object.id;
        }).catch(error => {
          if (uploads.get(dataUrl) === pending) uploads.delete(dataUrl);
          throw error;
        });
        uploads.set(dataUrl, pending);
      }
      return pending;
    },
  };
}

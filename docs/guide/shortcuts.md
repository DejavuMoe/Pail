# Command palette and shortcuts

Most of Pail can be used from the keyboard: the command palette finds any command, bucket or tool by name, and the main window has shortcuts for the common file operations. Context menus open from the keyboard as well.

## Command palette

Press Ctrl+K on Windows and Linux, or ⌘K on macOS. Ctrl+Shift+P (⌘⇧P) opens it too, and the same key closes it again. The palette is also the first entry of the **More** (⋯) menu.

Type part of a name in **Search commands, buckets and tools**. The search matches the English names of commands as well as the names in the current language, so `upload` finds **Upload files…** in Chinese too. Each result shows its group beside it.

### What it lists

Commands are grouped in this order:

| Group | Holds |
| --- | --- |
| **Transfer** | **Upload files…**, **Upload folder…** and **Download**. |
| **Object** | Actions on the selected objects, such as **New folder**, **Rename**, **Delete** and the copy commands. |
| **Navigate** | **Up one level**, **Refresh** and **Search**. |
| **View** | **Toggle inspector**, **Toggle transfers**, **Theme** and **Language**. |
| **Bucket** | **New bucket…** and **Bucket settings…**. |
| **Tools** | The tools of the sidebar, such as **Sync jobs** and **Upload rules**. |
| **App** | **Keyboard shortcuts**, **Documentation**, **About Pail**, **Quit**, and locking or unlocking the encrypted vault. |
| **Go to bucket** | Every bucket of the current account, by name or alias. The open bucket is checked. |

The list shows only the commands that can run at the moment. A search also finds the others, dimmed, for example **Download** with nothing selected.

### Theme and language

**Theme** and **Language** are one row each, with the current value beside the name. Choose one to see its values:

- **Theme**: **System**, **Light** or **Dark**.
- **Language**: **Follow system**, English or 简体中文.

The current value is checked. Press Backspace in the empty search box, or choose the arrow (**Back to all commands**), to go back. You can also type a value directly, such as `dark`, from the full list.

### Keys in the palette

| Key | Does |
| --- | --- |
| Up, Down | Moves through the results. |
| Page Up, Page Down | Moves eight rows at a time. |
| Enter | Runs the highlighted command. |
| Backspace | In an empty search box, goes back from theme or language values. |
| Esc | Closes the palette. |

## Keyboard shortcuts

These shortcuts work in the main window. On macOS, ⌘ replaces Ctrl, ⌥ replaces Alt and ⇧ replaces Shift. The full list is also in the app: choose **Keyboard shortcuts** in the command palette or in the **More** (⋯) menu.

| Command | Windows and Linux | macOS |
| --- | --- | --- |
| **Upload files…** | Ctrl+U | ⌘U |
| **Upload folder…** | Ctrl+Shift+U | ⌘⇧U |
| **Download** | Ctrl+D | ⌘D |
| **New folder** | Ctrl+Shift+N | ⌘⇧N |
| **Copy** | Ctrl+C | ⌘C |
| **Cut** | Ctrl+X | ⌘X |
| **Paste** | Ctrl+V | ⌘V |
| **Rename** | F2 | F2 |
| **Delete** | Del | Del |
| **Select all** | Ctrl+A | ⌘A |
| **Up one level** | Backspace or Alt+Up | Backspace or ⌥Up |
| **Refresh** | F5 or Ctrl+R | F5 or ⌘R |
| **Search** | Ctrl+F | ⌘F |
| **Toggle inspector** | Ctrl+I | ⌘I |
| **Toggle transfers** | Ctrl+J | ⌘J |
| **Command palette** | Ctrl+K or Ctrl+Shift+P | ⌘K or ⌘⇧P |

**Copy**, **Cut**, **Paste** and **Select all** act on objects only while no text field has the focus. In a text field they copy, cut, paste and select text as usual.

Menus and the palette write shortcuts as plain text, such as `Ctrl+Shift+U`.

### Selection

| Action | How |
| --- | --- |
| Clear the selection | Esc, or click the blank space below the rows. |
| Select a range | Shift-click the last row of the range. |
| Select with a rectangle | Drag from the blank space below the rows. |
| Keep the current selection while drawing | Hold Ctrl (⌘ on macOS) while you drag the rectangle. |

With two or more items selected, a bar under the table shows their count and size and offers **Download**, **Copy**, **Cut**, **Delete** and **Clear selection**.

## Context menus

Right-click an object, a folder or a bucket to open its menu. A right-click on a row that is not selected selects it first. The object menu lists only what applies to the selection:

- **Open**, **Download** and **Calculate size**;
- **Copy**, **Cut**, **Paste** and **Rename**;
- **Restore**, when **Show deleted** is on;
- storage class, encryption and archive restore;
- the **Copy link** submenu, with the key, URI, URL, CDN URL and presigned URL;
- **Open CDN URL** and CloudFront invalidation, where they apply;
- **Delete**, shown in red.

**Upload files…**, **New folder**, **Refresh** and **Select all** appear only when you right-click the empty space below the rows.

On Windows and Linux these menus are drawn inside the window and show the icon and shortcut of each command. On macOS they are system menus.

### Keyboard access

On Windows and Linux, press Shift+F10 or the menu key to open the context menu below the current row. The selection does not change.

| Key | Does |
| --- | --- |
| Up, Down | Moves through the items, skipping separators and unavailable items. |
| Home, End | Goes to the first or last item. |
| A letter | Goes to the next item that starts with it. |
| Right, Enter | Opens a submenu. |
| Left | Closes a submenu. |
| Enter | Runs the highlighted item and closes the menu. |
| Esc | Closes the submenu, then the menu. The focus goes back to the object table. |

The same keys work in the menus of the Upload button, the account switcher and the **More** (⋯) button.

## The More menu

The **More** (⋯) button holds the **Command palette**, **Settings**, **Theme** and **Language** submenus, **Keyboard shortcuts**, **Documentation**, **About Pail** and **Quit**.

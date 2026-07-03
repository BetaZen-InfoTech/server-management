import { useEffect } from 'react'
import { useEditor, EditorContent } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import Link from '@tiptap/extension-link'
import Image from '@tiptap/extension-image'
import { MenuBar } from './EditorMenuBar'

// RichEditor — a controlled WYSIWYG HTML editor (tiptap + the shared MenuBar).
// `value` is HTML; `onChange` fires with the new HTML on every edit. Used for
// the email-signature editor so users format visually instead of hand-writing
// raw HTML in a textarea.
export default function RichEditor({
  value,
  onChange,
  minHeight = 180,
  placeholder,
}: {
  value: string
  onChange: (html: string) => void
  minHeight?: number
  placeholder?: string
}) {
  const editor = useEditor({
    extensions: [StarterKit, Link, Image],
    content: value || '<p></p>',
    onUpdate: ({ editor }) => onChange(editor.getHTML()),
    editorProps: {
      attributes: {
        class: `prose max-w-none focus:outline-none px-3 py-2`,
        style: `min-height:${minHeight}px`,
        ...(placeholder ? { 'data-placeholder': placeholder } : {}),
      },
    },
  })

  // Keep the editor in sync when `value` is reset externally (e.g. cleared
  // after saving). setContent(false) doesn't emit an update, and the guard
  // avoids clobbering the caret while the user is typing.
  useEffect(() => {
    if (editor && value !== editor.getHTML()) {
      editor.commands.setContent(value || '<p></p>', false)
    }
  }, [value]) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <div className="border border-ink-200 rounded-lg overflow-hidden">
      <MenuBar editor={editor} />
      <EditorContent editor={editor} />
    </div>
  )
}

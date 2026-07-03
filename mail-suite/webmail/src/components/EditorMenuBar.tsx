import { ReactNode } from 'react'
import { Editor } from '@tiptap/react'
import {
  Bold, Italic, Strikethrough, Code, Heading1, Heading2, List, ListOrdered,
  Quote, Link2, Link2Off, Minus, Undo2, Redo2, Image as ImageIcon,
} from 'lucide-react'

// MenuBar — the shared rich-text formatting toolbar over a tiptap editor. Every
// button maps to an HTML-producing command (editor.getHTML() is the payload),
// so it covers real formatting: bold / italic / strike / inline code, H1-H2,
// bullet + numbered lists, blockquote, horizontal rule, links, and inline
// images by URL — all from StarterKit + the Link / Image extensions, no extra
// dependency. Used by both the mail composer and the signature editor.
export function MenuBar({ editor }: { editor: Editor | null }) {
  if (!editor) return null

  const Btn = ({ onClick, active, disabled, title, children }: {
    onClick: () => void; active?: boolean; disabled?: boolean; title: string; children: ReactNode
  }) => (
    <button
      type="button"
      title={title}
      disabled={disabled}
      onClick={onClick}
      className={`p-1.5 rounded transition-colors disabled:opacity-40 hover:bg-ink-100 ${
        active ? 'bg-brand-100 text-brand-700' : 'text-ink-600'
      }`}
    >
      {children}
    </button>
  )
  const Sep = () => <span className="w-px self-stretch bg-ink-200 mx-1" />

  const setLink = () => {
    const prev = editor.getAttributes('link').href as string | undefined
    const url = window.prompt('Link URL', prev || 'https://')
    if (url === null) return
    if (url === '') {
      editor.chain().focus().extendMarkRange('link').unsetLink().run()
      return
    }
    editor.chain().focus().extendMarkRange('link').setLink({ href: url }).run()
  }
  const addImage = () => {
    const url = window.prompt('Image URL', 'https://')
    if (url) editor.chain().focus().setImage({ src: url }).run()
  }

  return (
    <div className="flex flex-wrap items-center gap-0.5 border-b border-ink-200 bg-ink-50 px-2 py-1">
      <Btn title="Bold (Ctrl+B)" active={editor.isActive('bold')} onClick={() => editor.chain().focus().toggleBold().run()}><Bold size={16} /></Btn>
      <Btn title="Italic (Ctrl+I)" active={editor.isActive('italic')} onClick={() => editor.chain().focus().toggleItalic().run()}><Italic size={16} /></Btn>
      <Btn title="Strikethrough" active={editor.isActive('strike')} onClick={() => editor.chain().focus().toggleStrike().run()}><Strikethrough size={16} /></Btn>
      <Btn title="Inline code" active={editor.isActive('code')} onClick={() => editor.chain().focus().toggleCode().run()}><Code size={16} /></Btn>
      <Sep />
      <Btn title="Heading 1" active={editor.isActive('heading', { level: 1 })} onClick={() => editor.chain().focus().toggleHeading({ level: 1 }).run()}><Heading1 size={16} /></Btn>
      <Btn title="Heading 2" active={editor.isActive('heading', { level: 2 })} onClick={() => editor.chain().focus().toggleHeading({ level: 2 }).run()}><Heading2 size={16} /></Btn>
      <Btn title="Bullet list" active={editor.isActive('bulletList')} onClick={() => editor.chain().focus().toggleBulletList().run()}><List size={16} /></Btn>
      <Btn title="Numbered list" active={editor.isActive('orderedList')} onClick={() => editor.chain().focus().toggleOrderedList().run()}><ListOrdered size={16} /></Btn>
      <Btn title="Quote" active={editor.isActive('blockquote')} onClick={() => editor.chain().focus().toggleBlockquote().run()}><Quote size={16} /></Btn>
      <Btn title="Divider" onClick={() => editor.chain().focus().setHorizontalRule().run()}><Minus size={16} /></Btn>
      <Sep />
      <Btn title="Link" active={editor.isActive('link')} onClick={setLink}><Link2 size={16} /></Btn>
      <Btn title="Remove link" disabled={!editor.isActive('link')} onClick={() => editor.chain().focus().unsetLink().run()}><Link2Off size={16} /></Btn>
      <Btn title="Image by URL" onClick={addImage}><ImageIcon size={16} /></Btn>
      <Sep />
      <Btn title="Undo" disabled={!editor.can().undo()} onClick={() => editor.chain().focus().undo().run()}><Undo2 size={16} /></Btn>
      <Btn title="Redo" disabled={!editor.can().redo()} onClick={() => editor.chain().focus().redo().run()}><Redo2 size={16} /></Btn>
    </div>
  )
}

/**
 * Products page.
 *
 * ProTable list (pagination + status filter) plus a ProForm create/edit modal.
 * The gallery manager uploads to `/api/admin/v1/images` (2MB / JPEG-PNG-WebP
 * enforced client-side, re-enforced by the backend) and keeps an ordered list
 * of object keys: position 0 is the main image. An unchanged gallery is
 * omitted from the PATCH so the backend keeps it; clearing every card sends
 * an empty array. Every write action only renders when the session carries
 * the matching permission; the backend remains the only security boundary.
 */

import { useRef, useState } from 'react';
import { Button, Form, Image, message, Popconfirm, Space, Upload } from 'antd';
import { ArrowLeftOutlined, ArrowRightOutlined, DeleteOutlined, PlusOutlined, StarFilled, UploadOutlined } from '@ant-design/icons';
import {
  ModalForm,
  ProFormDigit,
  ProFormSelect,
  ProFormText,
  ProFormTextArea,
  ProTable,
} from '@ant-design/pro-components';
import type { ActionType, ProColumns } from '@ant-design/pro-components';
import { useAccess } from '@umijs/max';

import {
  createProduct,
  listProducts,
  updateProduct,
  uploadImage,
} from '@/services/products';
import type { Product, ProductStatus, ProductWriteRequest } from '@/services/types';
import { compressImage } from '@/lib/image-compress';
import { describeAdminError, withTraceId } from '@/requestErrorConfig';

const IMAGE_TYPES = new Set(['image/jpeg', 'image/png', 'image/webp']);
const IMAGE_MAX_BYTES = 2 * 1024 * 1024;
const MAX_IMAGES = 9;
/** Read APIs return public URLs; writes want object keys back. */
const IMAGE_URL_PREFIX = '/static/images/';

/** Mirrors the backend category catalog keys (product.Category). */
const CATEGORY_OPTIONS = [
  { label: '数码', value: 'digital' },
  { label: '家居', value: 'home' },
  { label: '美妆', value: 'beauty' },
  { label: '食品', value: 'food' },
  { label: '服饰', value: 'apparel' },
];

function keyFromUrl(url: string): string {
  const index = url.indexOf(IMAGE_URL_PREFIX);
  return index === -1 ? '' : url.slice(index + IMAGE_URL_PREFIX.length);
}

interface ProductFormValues {
  name: string;
  description?: string;
  category?: string;
  price_points: string;
  stock: number;
  status: ProductStatus;
}

/** Thumbnail card with per-image actions; the first card is the main image. */
function GalleryCard({
  url,
  isMain,
  onSetMain,
  onRemove,
  onMoveUp,
  onMoveDown,
  canManage,
}: {
  url: string;
  isMain: boolean;
  onSetMain: () => void;
  onRemove: () => void;
  onMoveUp: () => void;
  onMoveDown: () => void;
  canManage: boolean;
}): React.ReactElement {
  return (
    <div
      style={{
        position: 'relative',
        width: 86,
        border: isMain ? '2px solid #0958d9' : '1px solid #d9d9d9',
        borderRadius: 6,
        padding: 3,
      }}
    >
      <Image
        src={url}
        alt={isMain ? '主图' : '商品图'}
        width={78}
        height={78}
        style={{ objectFit: 'cover', borderRadius: 4 }}
        preview
      />
      {isMain && (
        <StarFilled
          aria-label="主图"
          style={{
            position: 'absolute',
            top: 6,
            left: 6,
            color: '#0958d9',
            background: 'rgba(255,255,255,0.9)',
            borderRadius: 4,
            padding: 2,
            fontSize: 12,
          }}
        />
      )}
      {canManage && (
        <div style={{ display: 'flex', justifyContent: 'center', gap: 2, paddingTop: 3 }}>
          <Button size="small" type="text" aria-label="左移" onClick={onMoveUp} disabled={isMain}>
            <ArrowLeftOutlined />
          </Button>
          {!isMain && (
            <Button size="small" type="text" aria-label="设为主图" onClick={onSetMain}>
              <StarFilled />
            </Button>
          )}
          <Button size="small" type="text" aria-label="右移" onClick={onMoveDown}>
            <ArrowRightOutlined />
          </Button>
          <Button size="small" type="text" danger aria-label="删除图片" onClick={onRemove}>
            <DeleteOutlined />
          </Button>
        </div>
      )}
    </div>
  );
}

export default function ProductsPage(): React.ReactElement {
  const access = useAccess();
  const canWrite = access['product:write'] === true;
  const canUpload = canWrite && access['image:write'] === true;

  const actionRef = useRef<ActionType>();
  const [form] = Form.useForm<ProductFormValues>();
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<Product | null>(null);
  const [imageUrls, setImageUrls] = useState<string[]>([]);
  const [initialUrls, setInitialUrls] = useState<string[]>([]);
  const [uploading, setUploading] = useState(false);

  function openCreate(): void {
    setEditing(null);
    setImageUrls([]);
    setInitialUrls([]);
    setModalOpen(true);
  }

  function openEdit(record: Product): void {
    const urls = record.images ?? [];
    setEditing(record);
    setImageUrls(urls);
    setInitialUrls(urls);
    setModalOpen(true);
  }

  function closeModal(): void {
    setModalOpen(false);
    setEditing(null);
    setImageUrls([]);
    setInitialUrls([]);
  }

  async function handleFinish(values: ProductFormValues): Promise<boolean> {
    const payload: ProductWriteRequest = {
      name: values.name.trim(),
      description: values.description?.trim() || undefined,
      category: values.category || undefined,
      price_points: String(values.price_points).trim(),
      stock: Number(values.stock),
      status: values.status,
    };
    // An untouched gallery is omitted so a PATCH keeps it; a changed gallery
    // (including clearing it) is sent as the full replacement list. The state
    // holds public URLs (what thumbnails render); writes convert back to keys.
    if (imageUrls.join('\n') !== initialUrls.join('\n')) {
      payload.images = imageUrls.map(keyFromUrl).filter(Boolean);
    }
    try {
      if (editing) {
        await updateProduct(editing.id, payload);
        message.success('商品已更新');
      } else {
        await createProduct(payload);
        message.success('商品已创建');
      }
      closeModal();
      actionRef?.current?.reload();
      return true;
    } catch (err) {
      message.error(withTraceId(describeAdminError(err)));
      return false;
    }
  }

  async function toggleStatus(record: Product): Promise<void> {
    const next: ProductStatus = record.status === 'on_sale' ? 'off_sale' : 'on_sale';
    try {
      await updateProduct(record.id, {
        name: record.name,
        price_points: record.price_points,
        stock: record.stock,
        status: next,
      });
      message.success(next === 'on_sale' ? '商品已上架' : '商品已下架');
      actionRef?.current?.reload();
    } catch (err) {
      message.error(withTraceId(describeAdminError(err)));
    }
  }

  const galleryColumns: ProColumns<Product>[] = [
    {
      title: '商品图',
      dataIndex: 'images',
      width: 100,
      // The search form derives its fields from column titles; image filter
      // inputs are meaningless noise.
      search: false,
      render: (_, record) => {
        const images = record.images ?? [];
        if (images.length === 0) {
          return <span style={{ color: 'rgba(0,0,0,0.45)', fontSize: 12 }}>无图</span>;
        }
        return (
          <Image.PreviewGroup>
            <div style={{ position: 'relative', display: 'inline-block' }}>
              <Image
                src={images[0]}
                alt={record.name}
                width={72}
                height={72}
                style={{ objectFit: 'cover', borderRadius: 6 }}
                loading="lazy"
              />
              {images.length > 1 && (
                <span
                  style={{
                    position: 'absolute',
                    right: -6,
                    bottom: -6,
                    background: 'rgba(0,0,0,0.65)',
                    color: '#fff',
                    fontSize: 11,
                    lineHeight: '16px',
                    padding: '0 5px',
                    borderRadius: 8,
                  }}
                >
                  {images.length} 张
                </span>
              )}
              {/* Extra gallery entries register into the same preview group. */}
              <div style={{ display: 'none' }}>
                {images.slice(1).map((src) => (
                  <Image key={src} src={src} alt={record.name} />
                ))}
              </div>
            </div>
          </Image.PreviewGroup>
        );
      },
    },
    { title: 'ID', dataIndex: 'id', width: 120, ellipsis: true },
    { title: '名称', dataIndex: 'name', ellipsis: true },
    {
      title: '分类',
      dataIndex: 'category',
      width: 90,
      search: false,
      render: (_, record) =>
        record.category
          ? (CATEGORY_OPTIONS.find((option) => option.value === record.category)?.label ?? record.category)
          : <span style={{ color: 'rgba(0,0,0,0.45)' }}>未分类</span>,
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      valueEnum: {
        on_sale: { text: '在售', status: 'Success' },
        off_sale: { text: '下架', status: 'Default' },
      },
    },
    { title: '价格（积分）', dataIndex: 'price_points', width: 120 },
    { title: '库存', dataIndex: 'stock', width: 80 },
    { title: '描述', dataIndex: 'description', ellipsis: true, width: 200 },
  ];
  if (canWrite) {
    galleryColumns.push({
      title: '操作',
      valueType: 'option',
      width: 140,
      fixed: 'right',
      render: (_, record) => [
        <a
          key="edit"
          href={`#/products?edit=${record.id}`}
          onClick={(e) => {
            e.preventDefault();
            openEdit(record);
          }}
        >
          编辑
        </a>,
        <Popconfirm
          key="toggle"
          title={
            record.status === 'on_sale'
              ? '确认下架该商品？下架后买家端不再展示。'
              : '确认上架该商品？'
          }
          onConfirm={() => void toggleStatus(record)}
        >
          <a
            href={`#toggle-${record.id}`}
            onClick={(e) => e.preventDefault()}
            style={{ marginLeft: 8 }}
          >
            {record.status === 'on_sale' ? '下架' : '上架'}
          </a>
        </Popconfirm>,
      ],
    });
  }

  const galleryFull = imageUrls.length >= MAX_IMAGES;

  function removeFromGallery(key: string): void {
    setImageUrls((keys) => keys.filter((item) => item !== key));
  }

  function moveInGallery(index: number, delta: -1 | 1): void {
    setImageUrls((keys) => {
      const target = index + delta;
      if (index === 0 || target < 1 || target >= keys.length) return keys;
      const next = [...keys];
      [next[index], next[target]] = [next[target], next[index]];
      return next;
    });
  }

  return (
    <>
      <ProTable<Product>
        rowKey="id"
        actionRef={actionRef}
        columns={galleryColumns}
        search={{ labelWidth: 'auto' }}
        pagination={{
          defaultPageSize: 20,
          pageSizeOptions: [10, 20, 50],
          showSizeChanger: true,
        }}
        request={async (params) => {
          try {
            const page = await listProducts({
              page: params.current ?? 1,
              page_size: params.pageSize ?? 20,
              status: params.status ? (params.status as ProductStatus) : undefined,
            });
            return { data: page.list, total: page.total, success: true };
          } catch (err) {
            message.error(withTraceId(describeAdminError(err)));
            return { data: [], total: 0, success: false };
          }
        }}
        toolBarRender={
          canWrite
            ? () => [
                <Button
                  key="create"
                  type="primary"
                  icon={<PlusOutlined />}
                  onClick={openCreate}
                >
                  新建商品
                </Button>,
              ]
            : undefined
        }
      />
      <ModalForm<ProductFormValues>
        title={editing ? `编辑商品 ${editing.id}` : '新建商品'}
        width={600}
        form={form}
        open={modalOpen}
        initialValues={
          editing
            ? {
                name: editing.name,
                description: editing.description,
                category: editing.category || undefined,
                price_points: editing.price_points,
                stock: editing.stock,
                status: editing.status,
              }
            : { status: 'on_sale' }
        }
        modalProps={{ destroyOnClose: true, onCancel: closeModal }}
        onOpenChange={(open) => {
          if (!open) closeModal();
        }}
        onFinish={handleFinish}
        submitter={{ searchConfig: { submitText: '保存' } }}
      >
        <ProFormText
          name="name"
          label="名称"
          placeholder="商品名称"
          rules={[{ required: true, message: '请输入商品名称' }]}
        />
        <ProFormTextArea
          name="description"
          label="描述"
          fieldProps={{ rows: 3 }}
          placeholder="商品描述（可选）"
        />
        <ProFormSelect
          name="category"
          label="分类"
          placeholder="不设置分类"
          allowClear
          options={CATEGORY_OPTIONS}
        />
        <ProFormText
          name="price_points"
          label="价格（积分）"
          placeholder="非负整数，如 100"
          rules={[
            { required: true, message: '请输入价格' },
            { pattern: /^\d+$/, message: '价格必须是非负整数' },
          ]}
        />
        <ProFormDigit
          name="stock"
          label="库存"
          min={0}
          rules={[{ required: true, message: '请输入库存' }]}
        />
        <ProFormSelect
          name="status"
          label="状态"
          options={[
            { label: '在售', value: 'on_sale' },
            { label: '下架', value: 'off_sale' },
          ]}
          rules={[{ required: true, message: '请选择状态' }]}
        />
        <Form.Item label="商品图片">
          <div>
            <Space wrap size={12}>
              {imageUrls.length === 0 && !canUpload && (
                <span style={{ color: 'rgba(0,0,0,0.45)', fontSize: 12 }}>未设置图片</span>
              )}
              {imageUrls.map((key, index) => (
                <GalleryCard
                  key={key}
                  url={key}
                  isMain={index === 0}
                  canManage={canUpload}
                  onSetMain={() =>
                    setImageUrls((keys) => [
                      key,
                      ...keys.filter((item) => item !== key),
                    ])
                  }
                  onRemove={() => removeFromGallery(key)}
                  onMoveUp={() => moveInGallery(index, -1)}
                  onMoveDown={() => moveInGallery(index, 1)}
                />
              ))}
              {canUpload && (
                <Upload
                  accept="image/jpeg,image/png,image/webp"
                  showUploadList={false}
                  disabled={galleryFull}
                  beforeUpload={(file) => {
                    if (imageUrls.length >= MAX_IMAGES) {
                      message.error(`最多 ${MAX_IMAGES} 张图片`);
                      return Upload.LIST_IGNORE;
                    }
                    if (!IMAGE_TYPES.has(file.type)) {
                      message.error('仅支持 JPEG / PNG / WebP 图片');
                      return Upload.LIST_IGNORE;
                    }
                    // Every pick goes through compressImage: it passes files
                    // already within BOTH limits (bytes and pixels) through
                    // untouched and re-encodes the rest in the browser.
                    const originalMB = (file.size / (1024 * 1024)).toFixed(1);
                    return compressImage(file)
                      .then(({ file: compressed, compressed: didCompress }) => {
                        if (!didCompress) {
                          return file;
                        }
                        const compressedMB = (compressed.size / (1024 * 1024)).toFixed(2);
                        message.info(
                          `原图 ${originalMB}MB，已压缩至 ${compressedMB}MB 后上传`,
                        );
                        // Returning the file replaces what customRequest uploads.
                        return compressed;
                      })
                      .catch(() => {
                        message.error('图片压缩失败，请换一张或压缩后重试');
                        return Upload.LIST_IGNORE;
                      });
                  }}
                  customRequest={async (options) => {
                    setUploading(true);
                    try {
                      const result = await uploadImage(options.file as File);
                      if (!result.url) {
                        message.error('上传响应缺少图片地址');
                        return;
                      }
                      setImageUrls((urls) =>
                        urls.includes(result.url) ? urls : [...urls, result.url],
                      );
                      message.success(`图片已上传（第 ${imageUrls.length + 1} 张）`);
                    } catch (err) {
                      message.error(withTraceId(describeAdminError(err)));
                    } finally {
                      setUploading(false);
                    }
                  }}
                >
                  <Button
                    icon={<UploadOutlined />}
                    loading={uploading}
                    disabled={galleryFull}
                    title={galleryFull ? `最多 ${MAX_IMAGES} 张` : undefined}
                  >
                    上传图片
                  </Button>
                </Upload>
              )}
            </Space>
            <div style={{ marginTop: 8, color: 'rgba(0,0,0,0.45)', fontSize: 12 }}>
              {canUpload
                ? galleryFull
                  ? `已达 ${MAX_IMAGES} 张上限；第一张为主图`
                  : `最多 ${MAX_IMAGES} 张，第一张为主图；JPEG/PNG/WebP，超过 2MB 自动压缩`
                : '上传图片需要 image:write 权限'}
            </div>
          </div>
        </Form.Item>
      </ModalForm>
    </>
  );
}

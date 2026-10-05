'use client';

// 模型卡片编辑 / 删除操作状态机，桌面 ModelItem 与移动 MobileModelItem 共用，
// 消除两份手写的表单草稿、校验与 mutation 样板。

import { useCallback, useState } from 'react';
import { useTranslations } from 'next-intl';
import { useDeleteModel, useUpdateModel, type ModelMarketItem } from '@/api/endpoints/model';
import { toast } from '@/components/common/Toast';
import { parsePriceDraft, priceDraftFromModel, type PriceDraft, type PriceField } from './pricing';

export function useModelActions(model: ModelMarketItem) {
    const t = useTranslations('model');
    const [isEditOpen, setIsEditOpen] = useState(false);
    const [isDeleteOpen, setIsDeleteOpen] = useState(false);
    const [editValues, setEditValuesRaw] = useState<PriceDraft>(() => priceDraftFromModel(model));
    const [invalidPriceFields, setInvalidPriceFields] = useState<readonly PriceField[]>([]);

    const updateModel = useUpdateModel();
    const deleteModel = useDeleteModel();

    /** 编辑中改动任意字段即清除校验错误，避免旧错误悬挂。 */
    const setEditValues = useCallback((next: PriceDraft) => {
        setEditValuesRaw(next);
        setInvalidPriceFields([]);
    }, []);

    const openEdit = useCallback(() => {
        setEditValuesRaw(priceDraftFromModel(model));
        setInvalidPriceFields([]);
        setIsDeleteOpen(false);
        setIsEditOpen(true);
    }, [model]);

    const closeEdit = useCallback(() => setIsEditOpen(false), []);

    const openDelete = useCallback(() => {
        setIsEditOpen(false);
        setIsDeleteOpen(true);
    }, []);

    const closeDelete = useCallback(() => setIsDeleteOpen(false), []);

    const saveEdit = useCallback(() => {
        const { prices, invalidFields } = parsePriceDraft(editValues);
        if (invalidFields.length > 0) {
            // 不静默清零：标出非法字段，由编辑弹窗内联提示。
            setInvalidPriceFields(invalidFields);
            return;
        }
        updateModel.mutate(
            { name: model.name, ...prices },
            {
                onSuccess: () => {
                    setIsEditOpen(false);
                    toast.success(t('toast.updated'));
                },
                // 失败提示统一由全局 MutationCache onError 兜底，避免重复弹 toast。
            },
        );
    }, [editValues, model.name, t, updateModel]);

    const confirmDelete = useCallback(() => {
        deleteModel.mutate(model.name, {
            onSuccess: () => {
                setIsDeleteOpen(false);
                toast.success(t('toast.deleted'));
            },
        });
    }, [model.name, t, deleteModel]);

    return {
        isEditOpen,
        isDeleteOpen,
        editValues,
        setEditValues,
        invalidPriceFields,
        openEdit,
        closeEdit,
        openDelete,
        closeDelete,
        saveEdit,
        confirmDelete,
        isUpdatePending: updateModel.isPending,
        isDeletePending: deleteModel.isPending,
    };
}
